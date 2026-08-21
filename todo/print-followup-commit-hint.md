# Print the follow-up commit hint after in-repo deletions

## Context

When saferm deletes tracked files inside a git repository it pre-stages the
deletions in the index; the sanctioned flow then completes with a safegit
commit of those deletions. The second half of the flow lives only in
operator knowledge -- saferm's output ends at the deletion.

## Problem

Sessions routinely stop after the delete, leaving staged deletions sitting
in the index for another session to trip over (or to be discovered much
later). The flow is two steps, and the tool only teaches the first.

## Solution

After a delete that staged deletions in a repository, print the exact
follow-up command, ready to paste, e.g.:

    staged 4 deletion(s); commit them:
      safegit commit -m "<why>" -- <dir-or-files>

- Use the deletion's --description as the suggested message where sensible.
- Print the narrowest correct pathspec (the common directory when all
  deletions share one, else the file list).
- Suppress under --quiet; in machine mode, carry the suggested command in
  the payload rather than prose.
- No behavior change to the deletion itself -- this is output only. Never
  refuse in-repo deletions: the staging integration is the feature, not a
  hazard.

## Affected areas

- The delete command's success output path
- Machine-mode payload (one added field)
- Tests for the hint's presence, pathspec narrowing, and --quiet
  suppression

## Effort estimate

Small.
