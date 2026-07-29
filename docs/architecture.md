---
title: Architecture
description: "How saferm's internal components fit together: archive storage, SQLite metadata, git integration, cross-device handling, and concurrency safety."
---

# Architecture

saferm replaces `rm` with a multi-stage deletion pipeline: archive the content, record metadata, and leave a trail that supports undo. This page explains how the internal components work together.

## Component overview

saferm is organized into four internal packages, each with a single responsibility:

| Package | Responsibility |
|---------|---------------|
| `internal/archive` | Physical file storage: moving files into the archive, compressing directories, restoring content |
| `internal/db` | Structured metadata: SQLite schema, CRUD operations, query filters, schema migrations |
| `internal/meta` | Context capture: environment variables, git state, parent process info, custom key-value pairs |
| `internal/git` | Git index management: detecting tracked files, staging removals on delete, staging additions on restore |

The top-level command handlers (`delete.go`, `undelete.go`, `purge.go`) orchestrate these packages. No package depends on another -- all coordination flows through the command handlers.

## Storage layout

All saferm data lives under a single root directory (default `~/.saferm/`, overridable via the `SAFERM_HOME` environment variable):

```
~/.saferm/
  archive/          # Archived file content
    <uuid>          # Regular file (renamed or copied from original)
    <uuid>.tar.zst  # Directory (compressed archive)
    <uuid>.symlink  # Symlink metadata (target path as plain text)
  db/
    saferm.db        # SQLite database (WAL mode)
    saferm.db-wal    # WAL journal (SQLite-managed)
    saferm.db-shm    # Shared memory (SQLite-managed)
  config.toml       # User configuration (managed by strictcli)
```

## Deletion lifecycle

A deletion passes through four stages: validation, archival, metadata recording, and git index update.

### 1. Validation

The `archive.Archive` function stats the target path and determines its type. Directories require the `-r` flag; without it, the operation fails with `ErrRecursiveRequired`. Missing files fail with `ErrFileNotFound` unless `-f` (ignore-missing) is set.

### 2. Archival

The archival strategy depends on the target type:

**Regular files.** The file is hashed (SHA-256, streaming) before being moved. The move uses `os.Rename` for an atomic same-filesystem operation. If `Rename` returns `EXDEV` (cross-device link error), the fallback path copies the file, verifies the copy's hash matches the pre-computed hash, and only then removes the original. The archived file is stored as `<uuid>` (no extension) in the archive directory.

:-: ref path="internal/archive" symbol="archiveFile"

**Directories.** The directory tree is walked to compute total size, then compressed into a `.tar.zst` archive (tar format with zstandard compression via `github.com/klauspost/compress/zstd`). The tar preserves relative paths, permissions, and symlinks within the tree. After successful compression and hashing of the archive file, the original directory is removed with `os.RemoveAll`. Partial archives are cleaned up on failure.

:-: ref path="internal/archive" symbol="archiveDirectory"

**Symlinks.** The symlink's target path is read via `os.Readlink` and written to a `.symlink` metadata file in the archive directory. The symlink itself is then removed. No content is archived because symlinks have no content -- the target path is sufficient for reconstruction.

:-: ref path="internal/archive" symbol="archiveSymlink"

### 3. Metadata recording

After successful archival, the command handler collects contextual metadata via the `meta` package and inserts a `DeletionRecord` into the SQLite database. The record captures:

- **Identity**: auto-increment ID, UUID (matches archive filename), original absolute path, original filename
- **Content**: file size (bytes), SHA-256 hash, directory flag, symlink target
- **Context**: deletion timestamp, user-provided description, optional original rm command, JSON metadata blob
- **Lifecycle**: `restored_at`/`restored_to` (set on undelete), `purged_at` (set on purge)

:-: ref path="internal/db" symbol="DeletionRecord"

The metadata JSON blob, produced by `meta.Collect`, includes:

- **Environment variables**: all env vars except those matching configurable denylist patterns (secrets, tokens, keys, credentials by default)
- **Git context**: current branch, HEAD SHA, and repository root -- empty strings when not in a git repo
- **Parent process**: PPID and parent command line (read from `/proc/<pid>/cmdline` on Linux, `ps` on macOS)
- **Custom pairs**: arbitrary key-value strings passed via `--meta key=value`

:-: ref path="internal/meta" symbol="Collect"

### 4. Git index update

When `--update-git-index` is true (the default) and the file resides in a git repository, saferm runs `git rm --cached` to stage the removal in the git index. This keeps the git working tree consistent with the filesystem without requiring a separate `git rm` step. The check uses `git ls-files --error-unmatch` to determine whether the file is tracked; untracked files are silently skipped.

:-: ref path="internal/git" symbol="GitRmCached"

## Restoration (undelete)

Restoration reverses the archival process:

- **Regular files**: `os.Rename` from archive to original path, with cross-device copy fallback
- **Directories**: extract the `.tar.zst` archive into the original path, stripping the top-level directory entry so contents land directly at the destination
- **Symlinks**: recreate the symlink via `os.Symlink` using the stored target, then remove the `.symlink` metadata file

The `Restore` function checks for conflicts at the destination. Without `--force-overwrite`, an existing file at the destination path causes `ErrConflict`. After successful restoration, the database record is updated with `restored_at` and `restored_to` timestamps.

If the destination is inside a git repository, `git add` is run to stage the restored file.

:-: ref path="internal/archive" symbol="Restore"

## Purge (permanent destruction)

Purging permanently removes the archived content while preserving the metadata record. The archive file (`<uuid>`, `<uuid>.tar.zst`, or `<uuid>.symlink`) is deleted from disk, and the database record's `purged_at` field is set. This means `saferm list --all` still shows the deletion history, but `saferm undelete` will refuse to restore a purged item because the content is gone.

Records can be selected for purging by ID, by age (`--older-than`), by size (`--larger-than`), or all at once (`--all`).

## Cross-device handling

When the source file and the archive directory reside on different filesystems (different mount points, network shares, container bind mounts), `os.Rename` fails with `EXDEV`. saferm detects this specific error by unwrapping the `*os.LinkError` and checking for `syscall.EXDEV`:

:-: ref path="internal/archive" symbol="isCrossDevice"

The fallback path for files is copy-and-verify: copy the content, compute the SHA-256 hash of the copy, compare it against the hash of the original. If the hashes match, the original is removed. If they do not match, the copy is deleted and the operation fails with `ErrHashMismatch`. This ensures no data loss even when atomic renames are unavailable.

:-: ref path="internal/archive" symbol="copyAndVerify"

Directories always use tar+zstd compression, which inherently handles cross-device scenarios because `createTarZst` reads the source tree and writes to the archive directory independently.

Restoration also handles cross-device: `restoreFile` attempts `os.Rename` first and falls back to copy-then-delete on `EXDEV`.

## Integrity verification

saferm uses SHA-256 hashing to verify file integrity at two points:

1. **At archival time**: every regular file is hashed before being moved or copied. The hash is stored in the database record. For directories, the hash covers the `.tar.zst` archive file itself.

2. **At cross-device copy time**: after copying a file to the archive, the copy is hashed and compared against the pre-archival hash. A mismatch aborts the operation and removes the corrupt copy.

Hashing is streaming (`io.Copy` into `crypto/sha256`), so memory usage is constant regardless of file size.

:-: ref path="internal/archive" symbol="hashFile"

## Concurrency model

saferm is designed for concurrent use by multiple processes (e.g., parallel AI agent sessions). Two mechanisms prevent conflicts:

### UUID-based archive naming

Every archived item receives a UUID v4 generated from `crypto/rand`. This guarantees unique filenames in the archive directory even when multiple saferm processes archive files simultaneously. There is no coordination between processes -- each generates its own UUID independently.

:-: ref path="internal/archive" symbol="generateUUID"

### WAL-mode SQLite

The database connection is opened with two pragmas passed via DSN:

- `journal_mode=WAL`: Write-Ahead Logging allows concurrent readers and a single writer without blocking. Multiple saferm processes can query the database while another is inserting a new deletion record.
- `busy_timeout=5000`: if a write lock is held by another process, SQLite retries for up to 5 seconds before returning `SQLITE_BUSY`. This handles the brief window where two processes try to insert simultaneously.

:-: ref path="internal/db" symbol="Open"

### Schema migrations

The database uses `PRAGMA user_version` to track schema version. Migrations are idempotent: each checks whether the target column already exists (via `PRAGMA table_info`) before issuing `ALTER TABLE`. This prevents errors when multiple processes race through the migration path on first run.

## Security considerations

### Tar extraction safety

The `extractTarZst` function includes path traversal protections. Every tar entry's path is validated:

- Absolute paths (starting with `/`) are rejected
- Parent directory references (`..`) are rejected
- After joining with the destination directory, the resolved absolute path is checked to ensure it remains within the destination tree

### Secret filtering

Environment variable capture excludes values matching configurable regex patterns. The default patterns filter out variables whose names contain `token`, `secret`, `password`, `key`, or `credential` (case-insensitive). Additional patterns can be added via the `--exclude-env-patterns` flag or `config.toml`.

### Archive permissions

The archive directory is created with mode `0700` (owner-only access), preventing other users on the system from reading archived content.
