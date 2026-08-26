#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
for mod in platform/neuroforge services/agent services/knowledge services/control; do
  echo "==> go test $mod"
  (cd "$ROOT/$mod" && go test ./...)
  echo "==> go vet $mod"
  (cd "$ROOT/$mod" && go vet ./...)
done
echo "==> shell syntax"
for script in "$ROOT"/scripts/*.sh; do
  sh -n "$script"
done
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  echo "==> docker compose config (base)"
  (cd "$ROOT" && docker compose --env-file .env.example config >/dev/null)
  echo "==> docker compose config (research profile)"
  (cd "$ROOT" && docker compose --env-file .env.example --profile research config >/dev/null)
fi
echo "validation OK"
