#!/usr/bin/env sh
set -eu
for k in NEUROFORGE_MASTER_URL NEUROFORGE_WORKER_TOKEN NEUROFORGE_GPU_WORKER_ID OLLAMA_MODEL OLLAMA_EMBEDDING_MODEL; do v=$(grep -E "^${k}=" .env | head -1 | cut -d= -f2- || true); [ -n "$v" ] || { echo "FEHLT: $k"; exit 1; }; echo "$v" | grep -q '192\.0\.2\.' && { echo "SETZEN: $k"; exit 1; } || true; done
command -v docker >/dev/null 2>&1 || { echo "FEHLT: docker"; exit 1; }
docker compose version >/dev/null
docker compose --profile monitoring config >/dev/null
command -v nvidia-smi >/dev/null 2>&1 || echo "WARNUNG: nvidia-smi nicht gefunden; NVIDIA Host/Toolkit prüfen."
echo "GPU subagent preflight: OK"
