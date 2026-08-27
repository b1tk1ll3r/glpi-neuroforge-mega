#!/bin/sh
set -eu
ENV_FILE=${ENV_FILE:-.env}
./scripts/preflight.sh
echo "go-live: pulling immutable registry images"
docker compose --env-file "$ENV_FILE" pull
echo "go-live: starting stack"
docker compose --env-file "$ENV_FILE" up -d --remove-orphans
echo "go-live: current container state"
docker compose --env-file "$ENV_FILE" ps
cat <<'MSG'
go-live: container start completed. Verify the host-specific smoke gates from docs/GO-LIVE-v1.5.0.md before enabling write automation.
MSG
