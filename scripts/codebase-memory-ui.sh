#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
PORT=${CODEBASE_MEMORY_PORT:-9749}
if ! command -v codebase-memory-mcp >/dev/null 2>&1; then
  echo "codebase-memory-mcp is not installed or not in PATH" >&2
  exit 1
fi
export CBM_ALLOWED_ROOT="$ROOT"
echo "Indexing $ROOT with CBM_ALLOWED_ROOT=$CBM_ALLOWED_ROOT" >&2
codebase-memory-mcp cli index_repository "{\"repo_path\":\"$ROOT\"}"
echo "Starting optional Codebase Memory UI on :$PORT" >&2
exec codebase-memory-mcp --ui=true --port="$PORT"
