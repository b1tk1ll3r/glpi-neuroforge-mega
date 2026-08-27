#!/bin/sh
set -eu
ENV_FILE=${ENV_FILE:-.env}
SNAPSHOT=${1:-}
HELPER_IMAGE=${BACKUP_HELPER_IMAGE:-busybox:1.36}
[ -n "$SNAPSHOT" ] && [ -d "$SNAPSHOT" ] || { echo "usage: $0 SNAPSHOT_DIR" >&2; exit 2; }
[ -f "$ENV_FILE" ] || { echo "restore: missing $ENV_FILE" >&2; exit 1; }
( cd "$SNAPSHOT" && sha256sum -c SHA256SUMS )
./scripts/preflight.sh
getv() { sed -n "s/^$1=//p" "$ENV_FILE" | tail -n1 | tr -d '\r'; }

restart() { docker compose --env-file "$ENV_FILE" up -d knowledge neuroforge neuroforge-worker agent >/dev/null 2>&1 || true; }
trap restart EXIT INT TERM

docker compose --env-file "$ENV_FILE" stop neuroforge-worker agent neuroforge knowledge
docker compose --env-file "$ENV_FILE" create neuroforge agent >/dev/null
volume_for() {
  service=$1; destination=$2
  cid=$(docker compose --env-file "$ENV_FILE" ps -aq "$service")
  [ -n "$cid" ] || return 1
  docker inspect -f "{{range .Mounts}}{{if eq .Destination \"$destination\"}}{{.Name}}{{end}}{{end}}" "$cid"
}
restore_volume() {
  service=$1; destination=$2; source=$3
  volume=$(volume_for "$service" "$destination")
  [ -n "$volume" ] || { echo "restore: volume for $service:$destination not found" >&2; exit 1; }
  src=$(CDPATH= cd -- "$source" && pwd)
  docker run --rm -v "$volume:/target" -v "$src:/source:ro" "$HELPER_IMAGE" sh -eu -c 'rm -rf /target/* /target/.[!.]* /target/..?* 2>/dev/null || true; cp -a /source/. /target/'
}
restore_volume neuroforge /app/data "$SNAPSHOT/neuroforge-data"
restore_volume agent /app/data "$SNAPSHOT/agent-data"
restore_host() {
  src=$1; dst=$2
  [ -d "$src" ] || return 0
  mkdir -p "$dst"
  find "$dst" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
  cp -a "$src"/. "$dst"/
}
restore_host "$SNAPSHOT/knowledge" "$(getv KB_DATA_PATH)"
restore_host "$SNAPSHOT/staging" "$(getv KB_STAGING_PATH)"
restore_host "$SNAPSHOT/knowledge-backups" "$(getv KB_BACKUP_PATH)"
echo "restore: snapshot restored; services will be restarted"
