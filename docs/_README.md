---
title: README.md
---
# saferm

AI-first safe rm replacement. Instead of deleting files, saferm archives them to `~/.saferm/` with rich metadata, supporting undelete.

AI agents delete files recklessly -- refactoring, cleaning up, retrying. Every deletion should be justified and reversible. saferm enforces this by requiring a `--description` flag on every delete, capturing environment context automatically, and making restoration trivial.

## Quick start

```
go install github.com/smm-h/saferm@latest
```

Or via Homebrew (macOS/Linux):

```
brew install smm-h/tap/saferm
```

Delete a file (the `--description` flag is mandatory):

```
saferm --yes delete --description "removing stale config" old-config.yaml
```

See what you've archived:

```
saferm list
```

Bring it back:

```
saferm --yes undelete old-config.yaml
```

## Install

| Method | Command |
|--------|---------|
| Go | `go install github.com/smm-h/saferm@latest` |
| Homebrew | `brew install smm-h/tap/saferm` |
| npm | `npm install -g saferemove` (not yet published) |
| PyPI | `pip install saferm` (not yet published) |

## Commands

:-: table-commands

## Example workflow

```
$ saferm --yes delete --description "broken migration, rewriting from scratch" -r db/migrations/
Archived db/migrations/ (id: 3)

$ saferm list
ID  PATH                   SIZE   DELETED
3   db/migrations/         14K    2 minutes ago

$ saferm info 3
ID:          3
Path:        /home/user/project/db/migrations/
Size:        14382
Type:        directory
Description: broken migration, rewriting from scratch
Deleted:     2026-05-16 14:32:01 UTC
Git branch:  feature/new-schema
Git HEAD:    a1b2c3d
Parent PID:  12345
Parent cmd:  claude

$ saferm --yes undelete 3
Restored db/migrations/
```

## Metadata

Every deletion automatically captures:

- **Description** -- the mandatory `--description` flag
- **Git context** -- branch, HEAD commit, repo root (auto-detected)
- **Environment variables** -- filtered by a configurable denylist to exclude secrets
- **Parent process** -- PID and full command line of the calling process
- **Claude Code session** -- via `CLAUDE_CODE_SESSION_ID` env var, if present
- **Custom metadata** -- arbitrary key=value pairs via `--meta`

## Storage

```
~/.saferm/
  archive/       files stored by UUID; directories as .tar.zst
  db/saferm.db   SQLite database (WAL mode)
  config.toml    optional configuration
```

Override the base directory with the `SAFERM_HOME` environment variable. `SAFERM_HOME` is *location infrastructure* -- the same category as `HOME` -- not a config value: it selects where saferm lives. Unlike config-file and environment *values*, `SAFERM_HOME` is not suppressed by `--hermetic`.

## Configuration

Optional file at `~/.saferm/config.toml`:

```toml
archive_dir = "/custom/archive"
db_path = "/custom/db.sqlite"
exclude_env_patterns = [
  "(?i)token",
  "(?i)secret",
  "(?i)password",
  "(?i)key(?!BOARD)",
  "(?i)credential",
]
```

The `exclude_env_patterns` list controls which environment variables are redacted from captured metadata.

A malformed `config.toml` is a hard error (exit 1) reporting the parse position, never silently ignored. Unknown keys are rejected, and for `archive_dir`/`db_path`, passing a CLI value that diverges from the config value is a hard error rather than silently letting one win. This conflict check only fires when the global flag is given in the pre-command position (`saferm --archive-dir X delete ...`); a post-command placement (`saferm delete --archive-dir X`) is not currently conflict-checked. `--hermetic` suppresses config-file and environment *values*, falling back to defaults -- but it does not touch `SAFERM_HOME`, which is infrastructure, not configuration.

## Concurrency

saferm is safe for concurrent use. SQLite WAL mode with `busy_timeout` and UUID-based archive naming mean multiple AI sessions (or humans) can delete files simultaneously without conflicts.

## Exit codes

:-: table-exit-codes

Config-layer failures -- a malformed `config.toml`, an unknown key, or a CLI value that conflicts with `archive_dir`/`db_path` in the config -- exit **1** (they are reported by the CLI framework before saferm runs). saferm's own semantic conflicts exit **7**. The distinction: exit 1 means the configuration could not be loaded or reconciled; exit 7 means saferm ran and hit a semantic conflict.

## Platforms

Linux and macOS (amd64, arm64).

## License

MIT

## Links

- GitHub: https://github.com/smm-h/saferm
- Docs: https://saferm.smmh.dev
