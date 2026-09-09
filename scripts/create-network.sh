#!/usr/bin/env sh
set -eu
NETWORK="${CORE_NETWORK:-glpi-ai-core}"
if docker network inspect "$NETWORK" >/dev/null 2>&1; then
  echo "Docker-Netzwerk $NETWORK existiert bereits."
else
  docker network create "$NETWORK" >/dev/null
  echo "Docker-Netzwerk $NETWORK wurde angelegt."
fi
