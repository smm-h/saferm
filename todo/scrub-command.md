# `saferm scrub`: permanently remove archived records and their metadata

Successor to `todo/.done/purge-preserves-metadata.md` (split 2026-08-07): its
item 1 shipped — purge now preserves the metadata record (`list --all` shows
`purged`, `info` works on purged ids, `undelete` refuses with the purged
status) and is pinned by `TestPurge_PreservesMetadata`. This file carries the
unshipped item 2.

## Problem

With purge preserving metadata by design, there is no way to permanently
remove a record at all — including its path, description, and environment
metadata. For genuinely sensitive entries (a path or description that itself
reveals something), retention-limited archives, or plain hygiene after years
of use, an explicit terminal operation is missing.

## Shape (from the original design discussion)

`saferm scrub <id-or-path>` (and perhaps `--older-than`): deletes the metadata
row AND any remaining archived content for the record. Consequential
(framework confirm gate — this is the one genuinely unrecoverable saferm
operation); `--dry-run` renders the records that would be scrubbed; `--quiet`
never suppresses the scrub listing. Distinct vocabulary from `purge`
(content-only) must stay crisp in help text and docs.

## Effort

Small-medium: one command + store deletion + classification/consequential
declarations + red-green tests + docs.
