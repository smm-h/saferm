# Symlink archive bug: standalone symlinks archived as dangling links

## Problem

When archiving a standalone symlink on the same filesystem as the archive directory, `archiveFile` uses `os.Rename` which moves the symlink itself (not the target content) into the archive. The symlink then dangles because its relative target path is meaningless from the archive directory. On undelete, `os.Stat` follows the dangling symlink and reports "archive entry not found", or if the symlink accidentally resolves, restores a pointer to stale/empty content.

Discovered during a real workflow: a symlink `native/android/.../ui.js -> ../../../runtime/ui.js` was deleted with saferm, then undelete restored a 0-byte result despite the DB recording 27,151 bytes.

## Seven interacting bugs

1. **No symlink detection in `Archive()`** -- only branches on `IsDir()`, never checks `ModeSymlink`.
2. **Wrong size in DB** -- `archiveFile` uses `Lstat` which returns the symlink string length (e.g., 43 bytes for the target path string), not the target content size.
3. **Hash/content mismatch** -- `hashFile` uses `os.Open` which follows the symlink (hashes real content), but the archive stores the raw symlink. The recorded hash doesn't match what's physically archived.
4. **Same-device rename moves the symlink** -- `os.Rename` moves the symlink entry, not the content it points to.
5. **Restore fails on dangling symlinks** -- `restoreFile` uses `os.Stat` (follows symlinks) to verify the archive entry exists. Dangling symlinks cause "archive entry not found".
6. **No symlink type in schema** -- `schema.go` only has `is_directory`. No way to identify archived symlinks.
7. **No standalone symlink tests** -- `archive_test.go` has `TestArchive_Symlink` but it only tests symlinks inside directories (where the directory is archived as a whole). Standalone symlink archival is untested.

## Cross-device path works correctly by accident

When the archive is on a different filesystem, the `EXDEV` fallback uses `os.Open` + copy + remove, which follows the symlink and archives real content. Only the same-device `os.Rename` path is broken.

## Fix

In `archiveFile`, before `os.Rename`, check `os.Lstat(path).Mode()&os.ModeSymlink`. If the path is a symlink, use the copy-and-remove path (same as the cross-device fallback) instead of rename. This dereferences the symlink and archives the actual file content.

Also fix size computation: use `os.Stat` (follows symlinks) instead of `os.Lstat` when the path is a symlink, so the DB records the correct content size.

In `restoreFile`, use `os.Lstat` instead of `os.Stat` at the archive entry existence check (defense-in-depth for pre-fix archives that contain dangling symlinks).

Optionally add `is_symlink` to the DB schema and store the original target path for informational purposes.

## Tests needed

- `TestArchive_Symlink_Standalone`: create a real file, symlink to it, archive the symlink, verify the archive contains real content (not a symlink), verify undelete restores the content as a regular file.
- `TestArchive_Symlink_Standalone_RelativePath`: same but with a relative symlink target.
- `TestArchive_Symlink_DanglingRestore`: archive a symlink, delete the original target, undelete -- should still restore the content (since it was archived by value).

## Reproduction

```bash
echo "real content" > /tmp/saferm-test-target.txt
ln -sf /tmp/saferm-test-target.txt /tmp/saferm-test-link.txt
saferm delete --description "test symlink" /tmp/saferm-test-link.txt
# Archive now contains a symlink, not the content
saferm undelete <id>
# Fails with "archive entry not found" or restores wrong content
```
