# Archive copy inverts memory relief when deleting tmpfs files under memory pressure

## Context

saferm's core design is archive-before-delete: every deleted file is first copied
into the archive, then unlinked. This is what makes deletions recoverable via
`undelete`.

On Linux, `/tmp` is commonly a tmpfs, so files there live in RAM (or in swap once
the kernel evicts their pages). A major use case for deleting from `/tmp` is
reclaiming memory on a system that is running out of it.

## Problem

The archive copy must *read* every byte of the doomed files. For tmpfs files whose
pages were swapped out, this forces them to be swapped back into RAM before the
unlink frees anything. For a batch deletion, the peak memory cost is the entire
batch size, paid up front, with the relief arriving only at the end.

Consequence: on a memory-exhausted system, running `saferm delete` on several GB
of stale tmpfs files briefly makes memory pressure *worse* — at exactly the moment
deletion is most urgent. This was observed in practice: a multi-GB tmpfs deletion
on a system with zero free swap coincided with the kernel OOM-killing an unrelated
process while the archive copy was in flight. A plain `rm` would have freed the
pages instantly without reading anything, but plain `rm` is exactly what saferm
exists to replace, so the tool needs to close this gap itself.

Note this is not limited to tmpfs: any large batch deletion doubles disk usage
transiently and thrashes the page cache. tmpfs is just where it becomes dangerous.

## Candidate solutions

### 1. Per-file archive-then-unlink ordering (bound the peak)

If the current implementation archives the whole batch before unlinking anything,
reorder to: for each file, copy to archive, fsync, unlink, then proceed to the
next. Peak transient cost drops from sum(batch) to max(single file). For
directories, walk bottom-up doing the same per file.

- Pros: simple, no new flags, no behavior change visible to users, bounds the
  hazard structurally rather than warning about it. Helps all filesystems, not
  just tmpfs.
- Cons: a mid-batch failure leaves the source half-deleted (some files archived
  and unlinked, some untouched). Needs a clear partial-failure story in the DB:
  entries recorded per file as they complete, so `undelete` can restore whatever
  was already archived. Worth checking whether the DB schema already supports
  partial batches.

### 2. Drop already-copied pages during the copy

While streaming a file into the archive, periodically call
`posix_fadvise(POSIX_FADV_DONTNEED)` on the copied range of the *source* (and/or
`madvise` if mmap-based). The doomed data then never accumulates in RAM beyond a
small window even within a single huge file.

- Pros: bounds peak cost below even the largest file; complements solution 1.
- Cons: fadvise on tmpfs has limited effect (tmpfs pages are anonymous-backed;
  DONTNEED semantics differ) — needs an experiment to confirm it actually
  releases pages there. More syscall plumbing in the copy loop. Go's stdlib copy
  paths would need replacing with an explicit loop.

### 3. Pre-flight memory check (hard error)

Before starting, compare total batch size against available memory
(`MemAvailable` + free swap). If archiving would plausibly exhaust it, hard-error
with a message explaining the mechanism and telling the agent to delete in
smaller batches.

- Pros: fits the hard-error-over-warning philosophy; prevents the OOM-adjacent
  case outright.
- Cons: blocking deletion when memory is low is the wrong failure mode if
  deletion is the *cure* for low memory — it can only be a complement to
  solutions 1/2 (with 1+2 in place, the threshold would be per-file, not
  per-batch, and would rarely trigger). Sizing the threshold well is fiddly;
  a bad threshold either never fires or blocks legitimate deletions. No bypass
  flag is allowed, so a false positive is a dead end for the agent — the
  threshold must be conservative.

### Recommended combination

Solution 1 is the structural fix and should be done regardless. Solution 2 is a
refinement worth an experiment on tmpfs specifically. Solution 3 only as a
per-file safeguard afterward, if still needed.

## Affected files

- `internal/archive/archive.go` — copy loop, batch ordering
- `delete.go` — orchestration of archive + unlink per batch
- `internal/db/` — if per-file completion records are needed for partial-failure
  recovery (solution 1)
- Tests: red-green — a test that simulates/asserts per-file interleaving order
  (e.g., unlink of file A happens before archive-read of file B), plus a
  partial-failure recovery test

## Effort estimate

- Solution 1: small-to-medium (reordering + partial-failure semantics + tests)
- Solution 2: small experiment first; medium if adopted (custom copy loop)
- Solution 3: small, but only meaningful after 1
