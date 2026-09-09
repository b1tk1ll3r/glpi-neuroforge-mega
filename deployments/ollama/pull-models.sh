#!/usr/bin/env sh
set -eu
[ -f .env ] && set -a && . ./.env && set +a
: "${OLLAMA_MODEL:=gemma4}"
: "${OLLAMA_EMBEDDING_MODEL:=embeddinggemma}"
docker compose exec ollama ollama pull "$OLLAMA_MODEL"
docker compose exec ollama ollama pull "$OLLAMA_EMBEDDING_MODEL"
echo "Modelle vorhanden:"
docker compose exec ollama ollama list
