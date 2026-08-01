# Consider exposing a public Go API

## Current state

All packages are under `internal/` -- saferm is a pure CLI binary with no importable Go API. Consumers shell out to the `saferm` binary.

## Question

Should saferm expose some packages as public Go API (move from `internal/` to a top-level `pkg/` or root-level packages)?

## Candidates

- `archive` -- archive/restore operations (tar+zstd for dirs, copy for files, symlink handling)
- `db` -- query archived items, metadata lookup
- `meta` -- environment metadata collection

## Tradeoffs

**For public API:**
- Other Go tools could import saferm directly instead of shelling out
- Pre-stable (0.x.x) so no stability commitment yet
- Would make `gen.exclude: ["*"]` in selfdoc.json worth removing (API reference pages would have value)

**Against:**
- Shelling out works fine for current consumers
- Public API means callers couple to internal types -- harder to refactor
- Added maintenance burden (backward compat expectations even in 0.x.x)

## If yes

1. Move selected packages from `internal/` to top-level (e.g., `archive/`, `db/`)
2. Remove `gen.exclude: ["*"]` from selfdoc.json
3. Run selfdoc gen to create API reference pages
