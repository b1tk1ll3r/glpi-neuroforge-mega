#!/usr/bin/env sh
set -eu
AGENT_URL="${AGENT_URL:-http://127.0.0.1:9980}"
KNOWLEDGE_URL="${KNOWLEDGE_URL:-http://127.0.0.1:9981}"
printf 'Agent /healthz: '
curl -fsS "$AGENT_URL/healthz" || true
echo
printf 'Agent /readyz: '
curl -fsS "$AGENT_URL/readyz" || true
echo
printf 'Knowledge /api/health: '
curl -fsS "$KNOWLEDGE_URL/api/health" || true
echo
