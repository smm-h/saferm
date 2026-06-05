---
title: CLAUDE.md
---
# saferm

Go CLI that replaces `rm` with safe archival to `~/.saferm/`. AI-first design: every deletion requires `--description` explaining why.

## Project structure

```
main.go          -- app setup, registers commands via strictcli
delete.go        -- saferm delete: archive files/dirs
undelete.go      -- saferm undelete: restore by ID or path
list.go          -- saferm list: show archived items
purge.go         -- saferm purge: permanently remove from archive
info.go          -- saferm info: full metadata for a deletion
helpers.go       -- humanSize, humanAge, parseDuration
exitcodes.go     -- exit codes 0-7
version.go       -- version from ldflags or debug.ReadBuildInfo

internal/
  archive/       -- file/dir archival (os.Rename, copy+verify for cross-device, tar+zstd for dirs)
  db/            -- SQLite database (WAL mode, busy_timeout=5000, CRUD operations)
  meta/          -- metadata collection (env vars, git context, PPID + parent cmdline)
  config/        -- TOML config loading (~/.saferm/config.toml), directory init
  test/          -- integration tests (builds binary, runs as subprocess)
  testutil/      -- test helpers

npm/             -- npm wrapper package (saferemove)
pypi/            -- PyPI wrapper package (saferm)
docs/            -- selfdoc source for saferm.smmh.dev
```

## Build and test

```bash
go build .                  # build
go test ./...               # all tests
go test ./... -short        # skip long tests
go install .                # install locally (picks up changes)
```

## Key conventions

- **CLI framework:** strictcli (`github.com/smm-h/strictcli/go/strictcli`). Commands use functional options. Handlers receive `map[string]interface{}`.
- **`--description` is mandatory** on delete (no default value in strictcli). Never add a default.
- **`-r` required for directories** (like rm).
- **`-f` skips errors** on nonexistent files and prompts.
- **Files** archived via `os.Rename` (or copy+verify for cross-device moves).
- **Directories** archived as `.tar.zst` (tar + zstandard compression).
- **SHA-256 hash** computed for integrity verification.
- **SQLite WAL** with `busy_timeout=5000` for concurrency safety.
- **All env vars captured** except those matching denylist patterns (secrets, tokens, keys, etc.).
- **Git context** auto-detected (branch, HEAD, root).
- **PPID + parent cmdline** captured (platform-specific: `proc_linux.go`, `proc_darwin.go`).
- **Config:** `~/.saferm/config.toml` (optional). Key field: `exclude_env_patterns` (regex list).
- **`SAFERM_HOME` env var** overrides `~/.saferm/` base dir. Used by tests for isolation.
- **Exit codes:** 0 (success), 1 (general), 2 (usage), 3 (file not found), 4 (permission), 5 (database), 6 (archive), 7 (conflict). Defined in `exitcodes.go`.
- **Version:** set via ldflags (`-X main.version=x.y.z`) at build time; falls back to `debug.ReadBuildInfo`, then `"dev"`.

## Testing

- Unit tests in each `internal/` package (`*_test.go`).
- Integration tests in `internal/test/` -- builds the binary, runs as subprocess, uses `SAFERM_HOME` for isolation.
- Pre-push hook runs `go test ./... -short -count=1`.

## Distribution

- Go binary (GoReleaser via GitHub Actions)
- npm wrapper: `saferemove` (in `npm/`)
- PyPI wrapper: `saferm` (in `pypi/`)
- Homebrew tap: `smm-h/tap/saferm`

## Tooling

- **rlsbl** for releases (`rlsbl release patch|minor|major --yes`)
- **safegit** for commits (`safegit commit -m "message" -- file1 file2`)
- **selfdoc** for docs site (saferm.smmh.dev)
- **strictcli** for CLI framework

## How AI agents should use saferm

saferm is designed for AI agents to use instead of `rm`. Always provide a meaningful `--description`.

### Basic usage

```bash
# Delete a file
saferm delete --description "Removing stale config after migration" old-config.yaml

# Delete a directory (requires -r)
saferm delete -r --description "Removing build artifacts after successful CI" ./build/

# Delete with force (ignore nonexistent files)
saferm delete -f --description "Pruning stale cache files" cache/*.json

# Record the original rm command that was replaced
saferm delete --description "Cleaning temp test output" --command "rm -rf test-output/" -r test-output/

# Add custom metadata
saferm delete --description "Removing deprecated module" --meta reason=deprecated --meta ticket=PROJ-123 old-module.go
```

### Other commands

```bash
# List archived items
saferm list
saferm list --all              # include restored items
saferm list --path "/home/*"   # filter by glob

# Show full metadata for a deletion
saferm info 42

# Restore a file (by ID or path)
saferm undelete 42
saferm undelete /path/to/file
saferm undelete --force 42     # overwrite existing file

# Permanently remove from archive
saferm purge 42 43 44          # by IDs
saferm purge --older-than 30d  # by age (h/d/w/m)
saferm purge --all -f          # everything, no prompt
```

### Guidelines for --description

The description should explain **why** the deletion is happening, not just what is being deleted. Good examples:

- "Removing build artifacts after successful CI"
- "Cleaning up temp test file created during debugging"
- "Deleting deprecated config -- replaced by new TOML format"
- "Pruning stale cache files older than 30 days"

Bad examples (too vague):

- "cleanup"
- "removing file"
- "not needed"
