#!/bin/sh
set -eu
ENV_FILE=${ENV_FILE:-.env}
DEST_ROOT=${1:-./release-backups}
[ -f "$ENV_FILE" ] || { echo "backup: missing $ENV_FILE" >&2; exit 1; }
getv() { sed -n "s/^$1=//p" "$ENV_FILE" | tail -n1 | tr -d '\r'; }
tag=$(getv IMAGE_TAG)
stamp=$(date -u +%Y%m%dT%H%M%SZ)
dest="$DEST_ROOT/$stamp"
mkdir -p "$dest"

./scripts/preflight.sh
restart() { docker compose --env-file "$ENV_FILE" up -d knowledge neuroforge neuroforge-worker-cpu neuroforge-worker-gpu agent >/dev/null 2>&1 || true; }
trap restart EXIT INT TERM

echo "backup: entering short maintenance stop"
docker compose --env-file "$ENV_FILE" stop neuroforge-worker-cpu neuroforge-worker-gpu agent neuroforge knowledge
docker compose --env-file "$ENV_FILE" create neuroforge agent >/dev/null
mkdir -p "$dest/neuroforge-data" "$dest/agent-data" "$dest/knowledge" "$dest/staging" "$dest/knowledge-backups"
docker compose --env-file "$ENV_FILE" cp neuroforge:/app/data/. "$dest/neuroforge-data/"
docker compose --env-file "$ENV_FILE" cp agent:/app/data/. "$dest/agent-data/"

copy_host() {
  src=$1; out=$2
  [ -d "$src" ] || return 0
  cp -a "$src"/. "$out"/
}
copy_host "$(getv KB_DATA_PATH)" "$dest/knowledge"
copy_host "$(getv KB_STAGING_PATH)" "$dest/staging"
copy_host "$(getv KB_BACKUP_PATH)" "$dest/knowledge-backups"
printf 'IMAGE_TAG=%s\nCREATED_AT=%s\n' "$tag" "$stamp" > "$dest/MANIFEST"
( cd "$dest" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS )
echo "backup: verified snapshot written to $dest"
