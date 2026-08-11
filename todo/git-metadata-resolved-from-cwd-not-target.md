# Git metadata resolves from saferm's CWD, not the target file's directory

## Context

The delete flow decides whether to run the git index update by checking
`metadata.GitRoot != ""` plus `gitutil.IsGitTracked(absPath)`
(delete.go:167-174). `IsGitTracked` correctly runs with `cmd.Dir` set to
the file's parent (internal/git/git.go). But the metadata capture that
produces `GitRoot` (internal/meta/meta.go:75-90, `runGitCmd`) sets no
`cmd.Dir` at all, so branch/HEAD/root resolve against whatever directory
saferm happens to be invoked from.

## Problem

Two consequences:

1. **Silently skipped index update.** Deleting a tracked file while the
   shell's CWD is outside any git repo (or in a different repo) yields
   `GitRoot == ""` for a file that IS in a repo, so the `git rm --cached`
   step is skipped with no error and no warning. The file stays in the
   index, and the next commit resurrects a path whose content is gone —
   exactly the failure the index update exists to prevent. This is
   silent degradation: the same command does different things depending
   on invocation CWD.
2. **Wrong audit metadata.** The archive entry records the branch/HEAD
   of the CWD's repo (or nothing), not the deleted file's repo — the
   audit trail lies about where the deletion happened.

## Solution

Resolve all per-target git metadata with `cmd.Dir` = the target's parent
directory, per file (a multi-file delete can span repos, so metadata is
per-target, not per-invocation). The gating for the index update should
use the same per-target resolution. Keep any invocation-level metadata
(CWD, env) separate from per-target repo metadata.

Red-green: a test that deletes a tracked file from a CWD outside the
repo must currently fail to update the index (red), then pass after the
fix (green). A second test asserts the archive entry records the
target's repo root/branch/HEAD, not the CWD's.

## Affected files

- `internal/meta/meta.go` (runGitCmd and the capture sites, ~lines 75-90)
- `delete.go` (index-update condition)
- Possibly the archive record schema (per-target vs per-invocation
  metadata split)
- Tests: `internal/meta`, plus a command-level regression test

## Effort

Small: plumbing `cmd.Dir` and splitting per-target capture; the schema
question is the only real design decision.
