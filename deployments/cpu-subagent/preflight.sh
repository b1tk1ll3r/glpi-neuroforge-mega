#!/usr/bin/env sh
set -eu
for k in NEUROFORGE_MASTER_URL NEUROFORGE_WORKER_TOKEN NEUROFORGE_CPU_WORKER_ID; do v=$(grep -E "^${k}=" .env | head -1 | cut -d= -f2- || true); [ -n "$v" ] || { echo "FEHLT: $k"; exit 1; }; echo "$v" | grep -q '192\.0\.2\.' && { echo "SETZEN: $k"; exit 1; } || true; done
command -v docker >/dev/null 2>&1 || { echo "FEHLT: docker"; exit 1; }
docker compose version >/dev/null
docker compose --profile monitoring config >/dev/null
echo "CPU subagent preflight: OK"
