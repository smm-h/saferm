# README install instructions say @latest

## Problem

The README's install table and quick-start line say
`go install github.com/smm-h/saferm@latest`. The fleet convention for
internal Go tools is `@v0` (or an exact `@v0.x.y`), never `@latest`: no
project here issues 1.x tags, so `@latest` can only ever resolve to
something that is not a real release, and the module proxy caches any
accidental phantom permanently.

## Fix

Replace every `@latest` in the README (and any other doc, template or
scaffold output) with `@v0`. Small: a few lines.
