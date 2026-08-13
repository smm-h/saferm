---
title: saferm
description: "saferm is an AI-first safe rm replacement that archives files instead of deleting them, capturing metadata and which tool invoked the deletion."
---

# saferm

AI-first safe rm replacement. Archives files instead of deleting them.

## CLI Reference

- [All commands and options](cli-index.md)

## Packages

saferm is organized into internal packages that handle distinct responsibilities: file and directory archival with integrity verification, SQLite-based metadata storage, git context detection, environment and process metadata capture, and reading the shared process trace store that answers which tool ran a deletion. Each package is independently testable and designed for concurrent use via WAL-mode SQLite and atomic file operations. Configuration is handled by strictcli's built-in config system (TOML format at `~/.saferm/config.toml`).

:-: ref path="internal/archive"

:-: ref path="internal/db"

:-: ref path="internal/git"

:-: ref path="internal/meta"

:-: ref path="internal/trace"
