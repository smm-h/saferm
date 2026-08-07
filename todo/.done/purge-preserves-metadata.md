# Purge should preserve metadata records

## Problem

`saferm purge` deletes both the archived file AND its metadata record from the archive. Once purged, there is no trace that the file ever existed — the audit trail is destroyed.

## Proposed behavior

Split purge into two operations:

1. **`saferm purge`** (default): deletes the archived file content but keeps the metadata record (original path, description, timestamp, environment, git context, parent process, session ID, custom meta). The record is marked as `purged` with a purge timestamp. `saferm list` shows purged entries with a `purged` status. `saferm info <id>` still returns full metadata. `saferm undelete` on a purged entry errors with "content has been purged."

2. **`saferm scrub`** (new, optional): permanently removes metadata records. This is the nuclear option — only for when the metadata itself needs to go (e.g., sensitive paths, GDPR). Could accept `--older-than 90d` for bulk cleanup.

## Why

The audit trail is the primary value of saferm over `rm`. Purging file content to reclaim disk space is reasonable. Destroying the metadata record defeats the purpose — you lose the "when was this deleted, why, by whom, in what context" information that makes saferm useful for forensics and accountability.

## Affected files

- Archive storage logic (file deletion vs record deletion)
- `purge` command (keep records, only delete content)
- New `scrub` command (delete records)
- `list` command (show purged entries with status)
- `info` command (show full metadata for purged entries)
- `undelete` command (error on purged entries)
