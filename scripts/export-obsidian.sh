#!/bin/sh
set -eu

MODE=${1:-kb}
OUT=${2:-./glpi-neuroforge-obsidian.zip}
TMP="${OUT}.tmp.$$"
trap 'rm -f "$TMP"' EXIT INT TERM

case "$MODE" in
  kb)
    BASE=${KB_URL:-http://127.0.0.1:8081}
    USER=${BASIC_AUTH_USER:-}
    PASS=${BASIC_AUTH_PASSWORD:-}
    URL="${BASE%/}/api/export/obsidian"
    if [ -n "$USER" ] || [ -n "$PASS" ]; then
      curl -fsS --user "$USER:$PASS" "$URL" -o "$TMP"
    else
      curl -fsS "$URL" -o "$TMP"
    fi
    ;;
  agent)
    BASE=${AGENT_URL:-http://127.0.0.1:8080}
    USER=${WEB_USERNAME:-}
    PASS=${WEB_PASSWORD:-}
    URL="${BASE%/}/api/knowledge/export/obsidian"
    if [ -n "$USER" ] || [ -n "$PASS" ]; then
      curl -fsS --user "$USER:$PASS" "$URL" -o "$TMP"
    else
      curl -fsS "$URL" -o "$TMP"
    fi
    ;;
  *)
    echo "usage: $0 [kb|agent] [output.zip]" >&2
    exit 2
    ;;
esac

mv "$TMP" "$OUT"
trap - EXIT INT TERM
printf 'written: %s\n' "$OUT"
