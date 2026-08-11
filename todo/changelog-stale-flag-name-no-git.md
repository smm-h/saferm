# Changelog 0.2.0 entry names a flag that no longer exists (`--no-git`)

## Context

The 0.2.0 entry for "automatic git index management on delete and
undelete" describes the suppression flag as `--no-git`. The flag was
later renamed to `--no-update-git-index` (strictcli flag-naming rules:
positive name + auto-negation), so the released changelog text now
points at a flag the binary rejects.

## Problem

A reader following the changelog tries `--no-git` and gets an
unknown-flag error. Minor, but the changelog is the user-facing record
and should not name dead surfaces.

## Solution

Amend the shipped entry through the changelog tooling (`rlsbl changelog
edit` selecting the entry, updating the description to name
`--no-update-git-index`), then regenerate. CHANGELOG.md is generated —
never edit it by hand.

## Affected files

- The 0.2.0-era entry in `.rlsbl/changes/` (via `rlsbl changelog edit`)
- Regenerated `CHANGELOG.md` / per-version `.md`

## Effort

Trivial: one entry edit plus regeneration.
