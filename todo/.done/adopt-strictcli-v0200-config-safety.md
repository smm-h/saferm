# Adopt strictcli v0.20.0 config safety primitives deliberately

## Context

saferm uses strictcli's config system (`WithConfig()` +
`WithConfigPath(~/.saferm/config.toml)` + TOML) for `archive_dir`, `db_path`,
and `exclude_env_patterns`. strictcli v0.20.0 overhauled the config
lifecycle, and some of it changes saferm's runtime behavior on the next
dependency bump whether or not saferm opts into anything.

## Problem

1. **Behavior change arrives implicitly with the bump.** On strictcli
   versions before v0.20.0, a malformed `config.toml` printed
   `warning: invalid TOML ... ignoring` and saferm silently ran with
   defaults — silent degradation (a user with a typo'd config gets default
   archive/db paths without noticing). In v0.20.0, malformed config is a
   hard error with parse position (exit 1). This is the correct behavior,
   but it should be adopted deliberately: tests exercised, docs/CLAUDE.md
   updated, and a changelog entry telling users that broken config files now
   fail loudly instead of being ignored.
2. **New opt-in primitives worth considering while at it:**
   - `WithConfigConflictMode("error")` — a flag set both in config.toml and
     on the CLI (or env) becomes a hard error instead of silent CLI-wins.
     For saferm's three config-managed values, silent CLI-wins is arguably
     fine (overriding `--archive-dir` ad hoc is legitimate); decide
     explicitly rather than by default.
   - `ctx.Source(name)` / `App.LastSources` — per-flag value provenance
     (cli/env/config/default), useful for diagnostics or `doctor`-style
     output.
   - Reserved-name enforcement: v0.20.0 panics at registration for global
     flags named `help`, `version`, `dump-schema`, `config`, `hermetic`,
     `mcp`; v0.18.0 banned bare `force` and `no-*` names. saferm should
     verify none of its registrations collide (note saferm has a `--force`
     SHORT `-f` on delete — check whether the ban covers the long name only).

## Proposed solution

One pass: bump strictcli to latest, run the full suite, add/adjust tests for
the malformed-config hard error (red first against expectations, per the
red-green policy), decide conflict-mode adoption explicitly (recommend
leaving `cli-wins` unless there is a concrete misuse story), update docs, and
release with a user-facing changelog entry about the malformed-config
behavior change.

- Pros: behavior change ships announced and tested instead of smuggled in by
  a routine dependency bump; aligns with the no-silent-degradation policy.
- Cons: none beyond a small test/docs pass.

## Affected files

- go.mod / go.sum (strictcli bump)
- main.go (only if adopting conflict mode / provenance)
- tests covering config loading; docs/_README.md or docs config section;
  CLAUDE.md config notes

## Effort estimate

Small: about an hour including tests and release.
