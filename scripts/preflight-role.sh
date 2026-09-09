#!/usr/bin/env sh
set -eu
ROLE="${1:-}"
case "$ROLE" in agent|knowledge|ollama|combined) ;; *) echo "Usage: $0 agent|knowledge|ollama|combined" >&2; exit 2;; esac
DIR="$(CDPATH= cd -- "$(dirname -- "$0")/../deployments/$ROLE" && pwd)"
ENVFILE="$DIR/.env"
[ -f "$ENVFILE" ] || { echo "Fehlt: $ENVFILE" >&2; exit 1; }
if grep -Eq '^[A-Z0-9_]+=CHANGE_ME' "$ENVFILE"; then
  echo "Hinweis: CHANGE_ME-Werte vorhanden:"
  grep -E '^[A-Z0-9_]+=CHANGE_ME' "$ENVFILE" | cut -d= -f1
  if [ "$ROLE" = agent ] || [ "$ROLE" = combined ]; then exit 1; fi
fi
mkdir -p "$DIR/../../runtime/agent-data" "$DIR/../../runtime/knowledge" "$DIR/../../runtime/backups" "$DIR/../../runtime/staging" "$DIR/../../runtime/ollama"
if command -v docker >/dev/null 2>&1; then
  (cd "$DIR" && docker compose config >/dev/null)
  echo "docker compose config: OK ($ROLE)"
else
  echo "Docker CLI nicht vorhanden; YAML wurde beim Release statisch geprüft."
fi
