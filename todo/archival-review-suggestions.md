# Suggestions from an archival-mechanism source review

## Context

A read-only review of the delete/undelete/purge path (`delete.go`, `undelete.go`,
`purge.go`, `helpers.go`, `identifiers.go`, `internal/archive/archive.go`,
`internal/db/`, `internal/meta/`) collected the suggestions below. Each item
stands alone and can be done independently. Anchors are function names rather
than line numbers so they stay valid as files change. Verify each claim against
current source before acting — the review was a point-in-time reading.

## Likely bugs

### Relative-path undelete lookup appears unable to match

`handleUndelete` passes the raw target string to the shared identifier
resolver, but delete records store `filepath.Abs` of the deleted path. A
relative path argument therefore seems unable to match by path, even from the
original working directory. The README quick start (`saferm undelete
old-config.yaml`) exercises exactly this form, so either the example is broken
or there is a matching path (e.g. by `original_name`) the review missed —
verify first.

- Fix, if confirmed: absolutize a path-form target before lookup
  (`filepath.Abs`), mirroring what `--destination` already does.
- Red-green: a test that deletes a file and restores it by relative path from
  the same working directory.
- Affected: `undelete.go` (`handleUndelete`), `identifiers.go` (resolver),
  tests.
- Effort: small.

### `purge` marks a record purged even when destroying the entry failed

A failed removal of the archive entry is only a warning, and `MarkPurged` still
runs. The row then claims the content is destroyed while the blob may still
exist on disk — `list`/`info` and the user's mental model disagree with
reality, and nothing ever retries.

- Option A (recommended): on a failed entry removal, do NOT mark the row
  purged; report the failure per record and exit non-zero (respecting the
  batch semantics). Re-running purge retries naturally.
  Pros: database never over-claims destruction. Cons: a permanently
  undeletable entry (e.g. permissions) keeps the record active — that is the
  honest state.
- Option B: add a distinct "purge failed" state on the record.
  Pros: visible history. Cons: schema change and a third state to reason
  about; likely overkill.
- Affected: `purge.go`, possibly `internal/db`.
- Effort: small.

## Security / permissions

### Database file is world-readable while holding a full environment capture

The metadata blob stores every environment variable not matching
`--exclude-env-patterns`. The live database file was observed as mode 0644. In
practice it is shielded by the 0700 parent directories, but the file itself
should not rely on that: a relaxed parent (or a backup/copy of the file)
exposes the capture. Related inconsistency: `ensureDirectories` creates
directories via a 0755 mkdir (umask-dependent) while `archive.Execute` asks
for 0700 — whichever runs first wins.

- Fix: open/create the SQLite file with 0600 (and chmod an existing one on
  open), and make all directory creation consistently 0700.
- Affected: `internal/db` (open path), `helpers.go` (`ensureDirectories`),
  `internal/archive/archive.go` (`Execute`).
- Effort: small.

## Durability / integrity

### No fsync anywhere on archived blobs

No `Sync` call exists in the tar writer, the copy fallback, or after entry
creation. Metadata durability rests on SQLite's WAL; blob durability on
nothing. Because the database insert happens before source removal, a crash
can leave a committed row whose blob never reached disk — and the source is
then removed on the strength of that row.

- Fix: fsync the entry (and the archive directory) after writing it and
  BEFORE the database insert, so a row can never reference a blob that a
  crash could erase. The hard-link path needs no data fsync (no bytes were
  written) but still benefits from a directory fsync.
  Pros: closes the row-without-blob crash window. Cons: measurable cost on
  the tar/copy paths; negligible on the common hard-link path.
- Affected: `internal/archive/archive.go` (`Execute`, `createTarZst`,
  `copyFile`).
- Effort: small-to-medium.

### Trees have no per-member digests

The recorded hash for a directory covers the `.tar.zst` container only.
`VerifyEntry` says so honestly, but the consequence is that corruption of a
single member inside a restored tree is undetectable from anything saferm
records. The pre-removal walk already records per-member size/mtime, and the
tar writer already streams every member's bytes, so a per-member SHA-256 is
nearly free to compute.

- Option A (recommended): write a manifest member (e.g. a JSON file under a
  reserved name) INTO the tar, listing each regular file's path, size, and
  SHA-256. Pros: self-describing — survives database loss; enables
  restore-time verification per member. Cons: reserved-name handling on
  extraction; format to specify.
- Option B: store the member digest map in the database record.
  Pros: no tar format change. Cons: lost with the database; grows rows for
  big trees.
- Affected: `internal/archive/archive.go` (`createTarZst`, `VerifyEntry`,
  extraction), possibly `internal/db`.
- Effort: medium.

### Special files inside trees are archived but silently dropped on restore

The tar writer gives fifos/devices headers via `tar.FileInfoHeader`, but
extraction restores only regular files, directories, and symlinks — fifos,
sockets, device nodes, and tar hard-link entries are silently skipped. A
delete-then-undelete round trip silently loses them, which is silent
degradation.

- Option A (recommended): refuse at archive time — the planning walk hard-errors
  naming the first special file it finds, so the delete never starts.
  Pros: no data loss possible; failure at the honest point. Cons: trees
  containing e.g. a stray socket (common in dev dirs) become undeletable via
  saferm until the special file is handled — arguably correct.
- Option B: archive and restore special file types properly.
  Pros: full fidelity. Cons: device nodes need privileges; complexity for a
  rare case.
- Option C: warn at archive time and record the skipped paths on the record.
  Cons: a warning is exactly the kind of signal agents ignore.
- Affected: `internal/archive/archive.go` (walk, `createTarZst`, extraction).
- Effort: small (A) / large (B).

### Bare-file entries are unrecoverable without the database

A tree survives database loss as a self-describing `.tar.zst` and a symlink
as a `.symlink` text file, but a plain file's entry is a bare UUID — its
original path, description, and hash exist only in the database.

- Option A: write a small `<uuid>.json` companion file next to each bare
  entry (original path, hash, deleted-at). Pros: archive becomes fully
  self-describing. Cons: doubles entry count; purge/undelete must manage the
  pair; a second place that can disagree with the database.
- Option B (recommended, cheaper): periodically (or on every delete) export a
  compact database snapshot into the archive directory, e.g. via SQLite's
  backup API or a JSONL dump. Pros: one artifact, covers all kinds, no
  per-entry bookkeeping. Cons: snapshot staleness window.
- Option C: accept and document the limitation.
- Affected: `internal/archive`, `internal/db`, `delete.go`.
- Effort: small (B) / medium (A).

## Fidelity / accounting

### Copy fallback and tar extraction lose mtime (and more)

The cross-device copy fallback preserves mode only; extraction sets mode from
the tar header but not mtime. Ownership/xattrs/ACLs are also dropped, but
mtime is the cheap, high-value one: `os.Chtimes` after copy/extract restores
it from data already in hand (lstat / tar header).

- Fix: restore mtime in `copyFile`, `CopyOut`, and tar extraction. Document
  that ownership and xattrs are not preserved.
- Affected: `internal/archive/archive.go`.
- Effort: small.

### Directory size accounting overstates purge reclaim

Recorded size for a tree is the uncompressed sum of member files, but the
on-disk entry is a compressed `.tar.zst`. `purge --dry-run`'s "freeing ~X"
therefore overstates actual reclaim, and `--larger-than` filters on logical
size without saying so.

- Fix: additionally record the on-disk entry size at archive time; use it for
  purge estimates, and document which size `--larger-than` filters on (or
  offer both).
- Affected: `internal/archive/archive.go`, `internal/db` (schema),
  `purge.go`, docs.
- Effort: small-to-medium (schema migration).

## Code structure

### Entry-path naming is derived in two places

The uuid/kind-to-filename mapping (`<uuid>` / `<uuid>.tar.zst` /
`<uuid>.symlink`) is encoded both in the archive package's plan construction
and in `archiveEntryPath` in `helpers.go`. The duplication is the finding:
reduce to a single authority — export one function from `internal/archive`
and have every caller (delete, undelete, info, purge) use it. A test
asserting the two agree is only a fallback, not the fix.

- Affected: `internal/archive/archive.go`, `helpers.go`, callers.
- Effort: small.

### Tar and symlink entry creation can silently truncate on name collision

The bare-file path is protected against a UUID collision by `os.Link`
failing with EEXIST (deliberately a hard error, not in the copy-fallback
set), but the `.tar.zst` and `.symlink` paths use plain create, which would
truncate an existing entry. Astronomically unlikely with crypto/rand UUIDs,
but the asymmetry is free to fix.

- Fix: open with `O_CREATE|O_EXCL` for both.
- Affected: `internal/archive/archive.go` (`createTarZst`, symlink archive
  path).
- Effort: trivial.

## Documentation

### Install instructions say `@latest`; the convention is `@v0`

`docs/_README.md` (the selfdoc template — never edit the generated
`README.md`) tells users `go install github.com/smm-h/saferm@latest`. This
module issues only 0.x tags, and `@latest` is exactly the form that breaks
permanently if a phantom or accidental 1.x tag ever reaches the module proxy.
Install instructions should read `@v0`. Sweep all docs (including npm/pypi
packaging text) for other `@latest` occurrences.

- Affected: `docs/_README.md`, any other docs mentioning the install command;
  regenerate with `selfdoc gen`.
- Effort: trivial.

### README claim "every archived path is named with both of its identifiers" is misleading

On disk, entries are named by UUID only; the numeric id appears only in the
printed `archived: [id] uuid path (size)` line and in the database. The
sentence reads as if the id were part of the filename. Reword to describe the
printed line, and point at the existing accurate sentence about UUID-named
storage.

- Affected: `docs/_README.md`; regenerate with `selfdoc gen`.
- Effort: trivial.
