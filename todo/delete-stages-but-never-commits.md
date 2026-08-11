# `saferm delete` stages tracked-file deletions but never commits them

## Context

Since 0.2.0 ("automatic git index management"), deleting a git-tracked
file archives it, unlinks it, and runs `git rm --cached` (delete.go:167-174,
internal/git/git.go:37-48; default `--update-git-index` true). `saferm
undelete` symmetrically runs `git add` on the restored path
(undelete.go:144-150, unconditionally). Neither command ever commits —
there is no `git commit` invocation anywhere in non-test source.

## Problem

Deleting a tracked file leaves a staged-but-uncommitted deletion sitting
in the repo. Under the fleet conventions this conflicts with:

- **Commit-immediately discipline**: every change is supposed to land as
  its own commit; a staged deletion is exactly the kind of dangling state
  the convention exists to prevent.
- **Multi-session worktree safety**: another session sharing the worktree
  sees a mysterious staged deletion it didn't make, can't tell whether it
  is in-progress work, and a clean-tree check (e.g. a release preflight)
  fails on it.
- **Accidental bundling**: the next unrelated `git commit` (non-pathspec)
  silently ships the deletion, so the removal ends up attributed to a
  commit about something else.

The staging itself is right (the registered intent — "a tracked file
that moved into the archive must leave the git index too, or the next
commit resurrects it" — is sound). The gap is the missing final step.

## Solutions

1. **Auto-commit, pathspec-scoped (recommended candidate).** After a
   successful index update, commit exactly the deleted paths
   (`git commit -m <msg> -- <paths>`, or via safegit for concurrency
   safety), with the message derived from the mandatory `--description`.
   - Pros: tree is clean after every delete; the audit trail (archive
     entry ↔ commit) becomes symmetric; description text is reused
     instead of retyped; pathspec scoping cannot sweep unrelated staged
     work.
   - Cons: saferm takes on commit policy (message format, safegit
     dependency or direct git); a caller mid-refactor may legitimately
     want to batch the deletion into a larger commit.
2. **Mandatory explicit choice for tracked files.** A required negatable
   flag (e.g. `--commit`/`--no-commit`) whenever the target is tracked,
   in the house mandatory-flags style; `--commit` performs solution 1's
   scoped commit, `--no-commit` documents that the caller owns the
   commit.
   - Pros: no implicit policy; forces the agent to think; both workflows
     (immediate commit, batch into larger commit) stay first-class.
   - Cons: extra friction on every tracked delete; multi-file deletes
     spanning repos need defined semantics.
3. **Status quo, documented.** Keep staging-only and state loudly in
   docs/help that the caller must commit.
   - Pros: zero code; maximum flexibility.
   - Cons: agents will forget; the dangling-staged-deletion class of
     mess keeps happening; relies on discipline, which the fleet
     philosophy explicitly distrusts.

The same decision applies to `undelete`'s `git add` (restore currently
leaves a staged modification/addition when the restored content differs
from HEAD).

## Affected files

- `delete.go` (index-update block, flag registration)
- `undelete.go` (restore-side counterpart)
- `internal/git/git.go` (new commit helper, if any)
- `docs/cli-delete.md`, `docs/cli-undelete.md`, `docs/architecture.md`
- Tests: `internal/git/git_test.go` plus command-level red-green tests

## Effort

Small-medium: the mechanics are a few dozen lines; the real work is the
policy decision (1 vs 2) and the multi-repo/multi-file edge cases, plus
tests for each path.
