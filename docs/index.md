---
title: saferm
description: "saferm is an AI-first safe rm replacement that archives files instead of permanently deleting them, with rich metadata capture and full undo support."
---

# saferm

AI-first safe rm replacement. Archives files instead of deleting them.

## CLI Reference

- [All commands and options](cli-index.md)

## Packages

saferm is organized into internal packages that handle distinct responsibilities: file and directory archival with integrity verification, SQLite-based metadata storage, git context detection, environment and process metadata capture, and TOML configuration loading. Each package is independently testable and designed for concurrent use via WAL-mode SQLite and atomic file operations.

:-: ref path="internal/archive"

:-: ref path="internal/db"

:-: ref path="internal/git"

:-: ref path="internal/meta"

:-: ref path="internal/config"
