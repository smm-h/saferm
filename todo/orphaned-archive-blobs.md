# Orphaned archive blobs: non-atomic delete flow, no reconciliation, no restore-time hash verification

## Context

A 6.6GB blob (`467d6ca9-….tar.zst`) was found in the archive directory with no
corresponding row in the deletions table. It was a truncated tar.zst (zstd:
"Read error (39): premature end") of a uv cache directory. The same source
directory had a second, intact, properly-recorded blob created 3 minutes later
the same day — i.e. a failed first attempt followed by a successful retry.
The recorded retry's provenance metadata shows the delete ran under an AI
agent's shell tool; the first attempt died at ~120s, consistent with the
tool's default timeout killing the process group mid-compression.

A full reconciliation of archive dir vs DB on the discovering machine found
3 orphan blobs total: the truncated 6.6GB one (removed after manual
verification) and two small INTACT ones that remain and represent real
archived content invisible to every saferm command:

- `e41dc20c-5542-4dd8-9a55-d75d2c27c4c2.tar.zst` — 8,521 B, contains a CMake `build/` tree
- `fe28e3f6-165c-4749-b202-45942c07d149.tar.zst` — 98 B, contains `ntfs-main/`

## Problem

The delete flow (`delete.go:109-198` → `internal/archive/archive.go:97-109`) is:

1. Plan (pure read)
2. `archive.Execute` — write blob **directly at its final name**, then `os.RemoveAll` source
3. `git rm --cached` if tracked
4. `database.Insert(rec)`

Failure windows (directory case, `archiveDirectory` at `archive.go:175-215`):

- **W1** killed during `createTarZst` (`archive.go:197`): truncated blob at final name, source intact, no row
- **W2** killed during `hashFile(dst)` (`archive.go:203`): intact blob, source intact, no row
- **W3** killed during `os.RemoveAll(path)` (`archive.go:210`): intact blob, source half-deleted, no row
- **W4** killed after Execute returns, before `database.Insert` (`delete.go:157-188`): intact blob, source GONE, no row — archived data invisible to list/undelete

Non-crash leaks: `os.RemoveAll` failure returns without removing dst
(`archive.go:210-212`); `database.Insert` failure (`delete.go:188-191`) exits
with blob on disk and source already gone; cross-device file copy where
removing the original fails (`archive.go:164-166`).

Aggravating factors:

- No temp-name + rename-into-place (file case `os.Rename` at `archive.go:158`
  is atomic; the directory case and the cross-device `copyFile` fallback
  (`archive.go:319-341`) are not).
- No signal handling anywhere (no `os/signal` imports); Go default disposition
  terminates without running deferred cleanup.
- No fsync anywhere in saferm; SQLite (WAL) does fsync, so on power loss a
  durable row can point at a non-durable blob.
- **Nothing ever enumerates the archive directory** (no `ReadDir`/`Glob` in
  non-test code): orphans are unreachable by every command, invisible in every
  listing, immune to every purge selector.
- No `doctor`/`fsck`/`gc`/`check` command exists (`WithChecks` never used).

Reverse hazard (row without blob): `undelete` fails cleanly
(`archive.go:251-254, 275-279`; exit 6) but the message doesn't say the DB and
archive disagree. Worse: **restore never verifies the stored `hash` column**
(`hashFile` is only called on the archiving side), so a corrupt/truncated blob
with a valid row extracts partially; with `--force-overwrite` the pre-existing
destination is deleted FIRST (`archive.go:225`), so a corrupt blob can destroy
a good file and leave a partial tree.

## Solutions

- **F1 — temp name + rename into place.** Write `<uuid>.tar.zst.part` (or
  `os.CreateTemp` in archiveDir), rename after hashing. Pros: kills W1; final
  name only ever appears on complete blobs; `.part` files are self-identifying
  sweepable garbage; small local change (`archive.go:195-201, 319-341`).
  Cons: doesn't address W2-W4.
- **F2 — pending-row two-phase insert.** Insert row in state `pending` before
  archiving, flip to `complete` after. Pros: every blob discoverable from the
  moment its name is minted; crash leaves a visible pending row naming the
  source path; recovery becomes mechanical. Cons: schema migration
  (`internal/db/schema.go`, `db.go:65-100` migration ladder); every reader
  must learn the state; interaction with the dry-run-never-writes invariant
  needs care.
- **F3 — signal handling with cleanup.** `signal.Notify` SIGINT/SIGTERM,
  cancel in-flight archive, remove partial dst. Pros: covers graceful kills
  (what tool timeouts typically send first); cheap. Cons: useless against
  SIGKILL/OOM/power loss; must not race existing error-path cleanup.
- **F4 — `saferm doctor` (or `check`) reconciliation command.** Diff archive
  dir against DB both directions; report orphan blobs (size, zstd integrity,
  tar top-level peek), rows with missing blobs, hash mismatches. Destructive
  orphan-removal subcommand and/or "adopt" mode minting rows for intact
  orphans. Pros: the only option that addresses orphans already on disk
  (including the two intact ones above); turns an invisible failure class into
  a reported one; composes with all others. Cons: new command surface; adopt
  can only infer the original path from the tar's top-level entry (full
  absolute path is not stored in the blob); destructive gc is itself
  consequential.
- **F5 — verify hash on restore; stage-then-rename restores.** Check
  `rec.Hash` before/while extracting (TeeReader to avoid a second full read);
  extract to a sibling temp dir and rename so failed restores leave the
  destination untouched. Pros: closes the corrupt-blob-destroys-good-file
  path; makes the hash column earn its keep. Cons: disk headroom for staging.
- **F6 — fsync blob + archive dir entry before DB insert.** Pros: closes the
  durable-row/non-durable-blob inversion on power loss. Cons: real throughput
  cost on multi-GB archives; doesn't help the process-kill case (the observed
  one).

F1+F3+F4+F5 compose well and need no schema change. F2 is the structurally
strongest fix for discoverability. F6 is independent hardening.

## Affected files

- `delete.go:109-198` (flow ordering)
- `internal/archive/archive.go:97-109, 122-142, 144-173, 175-215, 219-296, 319-341, 363-453` (Execute, per-kind archive/restore, copy, tar)
- `internal/db/schema.go`, `internal/db/db.go:65-100` (if F2/F4-adopt)
- `main.go:44-48` (new command registration if F4)
- `internal/test/` (regression tests: kill-mid-archive simulation, orphan detection, restore of truncated blob)

## Effort

- F1: small. F3: small. F5: small-medium. F6: small.
- F4: medium (new command + destructive-confirmation design).
- F2: medium-large (migration + all readers).

Red-green: simulate each window (kill child mid-createTarZst; inject Insert
failure; truncate a blob and attempt undelete with and without
--force-overwrite) before fixing.
