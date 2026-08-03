# CLAUDE.md documents `undelete --force`; the actual flag is `--force-overwrite`

Filed 2026-08-03.

## Problem

`CLAUDE.md:115` shows:

```
saferm undelete --force 42     # overwrite existing file
```

Per the current schema (`.strictcli/schema.json`), the flag is `--force-overwrite` — bare `--force` is banned framework-wide at registration time (qualified names required), so the documented form is a parse error. Agents copying the example hit an unknown-flag failure exactly when they are trying to recover a file.

## Work

1. Fix `CLAUDE.md:115` to `--force-overwrite`.
2. Sweep README and any docs for the same retired form (the `--force` ban migration likely left other instances).

## Affected files

`CLAUDE.md`, possibly `README.md`/docs.

## Effort

S.
