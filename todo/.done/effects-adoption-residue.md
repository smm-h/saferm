# Effects-regime adoption residue

Filed at the strictcli effects-regime migration visit (go-strictcli 0.28.2). The migration
landed: the `--verbose` global and `purge`'s own `--dry-run` are gone onto the framework pair,
all five commands are classified, and the archive/restore/purge mutations are minted on
`ctx.Effects()` so a dry run records and destroys nothing. Three things were found and
deliberately NOT done at that visit.

## 1. Two consent gates now sit in series on `purge`

`purge` has its own confirmation (`--skip-confirmation` / `-f`) AND is now gated by the
framework's confirm protocol (`--yes`). A non-interactive `saferm --yes purge --all` passes the
framework and then hits saferm's own prompt, reads EOF, prints `Aborted.` and exits **0** --
looking like a success that purged nothing. The correct non-interactive invocation is
`saferm --yes purge --all --skip-confirmation`, which is two flags saying the same thing.

`delete --interactive` is the same shape, one layer down: it prompts per file, and a piped
run silently skips every file.

Options: (a) drop saferm's own prompts and let `--yes` be the single consent, accepting that
the per-item listing disappears from the prompt; (b) keep the listing but make it informational
and gate solely on `--yes`; (c) leave both and document the pairing. (b) preserves everything
the prompt is actually for -- showing what is about to be destroyed -- while making one flag
mean one thing.

Affected: purge.go, delete.go, internal/test/purge_test.go.

## 2. Two mutations cannot be expressed in the closed method set

The effects handle has exactly eight methods, and two of saferm's mutations do not fit any:

- **The SQLite database.** Every `delete` insert, `undelete` restore-marking and `purge`
  purge-marking is a row change. `write` is whole-file bytes; nothing can describe a row. All
  database writes therefore sit outside the handle and are skipped in dry mode -- which is the
  right behaviour, but it means the would-do log does not mention that a dry run would also
  have written a record.
- **The compound archival itself.** Archiving a file is hash-then-rename with a
  copy-and-verify fallback across devices; archiving a directory is tar + zstd of a whole tree
  followed by a recursive removal. Neither is a single primitive, and minting `rename` for the
  file case would silently drop the cross-device fallback. `recordArchival` (delete.go) is
  therefore dry-mode-only: it describes the archival on the handle, and `archive.Execute`
  performs it in a real run. `undelete` carries the same split. Those are the only two places
  in the repo where the record and the act are separate calls.

Both are recorded upstream as a framework gap; nothing is pending here unless the method set
grows a streaming-write or verified-move primitive.

Affected: delete.go, undelete.go, purge.go, internal/archive/archive.go.

## 3. `ensureDirectories` still creates `~/.saferm` under `--dry-run`

Every command (including the read-only `list` and `info`) calls `ensureDirectories`, which
`MkdirAll`s the base, archive and database directories. It is saferm's own state directory --
the same category as a framework cache write -- so it is not minted on the handle and is not
suppressed in dry mode. A first-ever `saferm --dry-run list` therefore creates `~/.saferm/`.

That is defensible (the directory is inert and the alternative is failing a read-only command
on a fresh machine), but it is undeclared. Either mint it as a `mkdir` on the mutating commands
and probe-and-skip on the read-only ones, or document it as infrastructure setup.

Affected: helpers.go, delete.go, undelete.go, purge.go, list.go, info.go.

## Effort

1: half a day including the flag design decision. 2: nothing until the framework moves.
3: an hour.
