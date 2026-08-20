# Reversibility gap: `undelete --group` is missing

## Context

strictcli is gaining declared reversibility support: a mutating command will
declare which command undoes it (verified at registration in both
directions), a command with no recovery will declare irreversible with a
mandatory reason, a warn-severity check will flag destructive commands
declaring neither, and after a real run the framework will print a paste-able
recovery command and emit a machine-readable recovery member in the JSON
result document. When this repo adopts that support, every gap below needs
either a built inverse or an honest irreversible declaration.

## Problem

`delete` mints a `group_id` per invocation (delete.go, around the archive
loop), persists it on every record (internal/db/db.go and the schema), and
returns it in the payload (it is a required payload member) — so batches stay
recoverable as batches. But nothing consumes it: `undelete` accepts only a
single uuid, numeric id, or path. One delete of twelve files needs twelve
undeletes, and the identifier the tool went to the trouble of minting has
zero consumers.

## Solution

Add group recovery to `undelete` — either an `--group <group_id>` flag or a
group-id branch in the existing polymorphic identifier resolution (the path
branch already shows the candidate-table-plus-hard-error pattern for
ambiguity). Restores every not-yet-consumed record of the group; reports
per-record outcomes; consumed/purged records are named, not silently
skipped.

## Affected files

- undelete command registration and handler
- the identifier-resolution helper
- payload schema for the batch result
- tests (batch delete then group undelete round-trip)

## Effort

Small — pure wiring; the data model is already complete.
