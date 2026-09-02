#!/usr/bin/env sh
set -eu
bad=0
check() { key="$1"; val=$(grep -E "^${key}=" .env | head -1 | cut -d= -f2- || true); if [ -z "$val" ] || echo "$val" | grep -Eq 'CHANGE_ME|example\.invalid|192\.0\.2\.'; then echo "SETZEN: $key"; bad=1; fi; }
for k in GLPI_URL GLPI_CLIENT_ID GLPI_CLIENT_SECRET GLPI_USERNAME GLPI_PASSWORD OLLAMA_BASE_URL OLLAMA_URLS; do check "$k"; done
if [ "$bad" -ne 0 ]; then echo "Preflight fehlgeschlagen: Platzhalter ersetzen."; exit 1; fi
command -v docker >/dev/null 2>&1 || { echo "FEHLT: docker"; exit 1; }
docker compose version >/dev/null
docker compose --profile research --profile monitoring config >/dev/null
[ -f monitoring/prometheus/prometheus.yml.template ]
[ -f monitoring/prometheus/alerts.yml ]
[ -f monitoring/grafana/dashboards/neuroforge-master-subagents.json ]
mkdir -p knowledge staging backups
./monitoring/validate.sh
echo "Master preflight: OK"
