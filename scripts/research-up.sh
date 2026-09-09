#!/usr/bin/env sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
secret=${SEARXNG_SECRET:-}
if [ -z "$secret" ] && [ -f "$ROOT/.env" ]; then
  secret=$(awk -F= '$1=="SEARXNG_SECRET" {sub(/^[^=]*=/, ""); print; exit}' "$ROOT/.env")
fi
case "$secret" in
  ""|CHANGE_ME*)
    echo "Set a real SEARXNG_SECRET in $ROOT/.env (or export it) before enabling research." >&2
    exit 1
    ;;
esac
export SEARXNG_SECRET=$secret
cd "$ROOT"
NEUROFORGE_RESEARCH_ENABLED=true \
NEUROFORGE_SEARXNG_ENABLED=true \
docker compose --profile research up -d ollama knowledge searxng neuroforge neuroforge-worker-cpu neuroforge-worker-gpu
printf '%s\n' 'SearXNG + NeuroForge research are running. Autonomy remains controlled by NEUROFORGE_AUTONOMY_ENABLED.'
