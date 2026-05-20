#!/usr/bin/env bash
set -euo pipefail

echo "  Updating CLI schema..."
go run . --dump-schema
