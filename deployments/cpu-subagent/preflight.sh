#!/usr/bin/env sh
set -eu
[ -f .env ] || { echo 'missing .env'; exit 1; }
for k in IMAGE_TAG NEUROFORGE_MASTER_URL NEUROFORGE_WORKER_TOKEN NEUROFORGE_CPU_WORKER_ID; do
  v=$(awk -F= -v key="$k" '$1==key {sub(/^[^=]*=/,""); print; exit}' .env)
  [ -n "$v" ] || { echo "missing: $k"; exit 1; }
done
if grep -Eq '192\.0\.2\.|example\.invalid|CHANGE_ME|YOUR-' .env; then echo 'placeholder/example values remain in .env'; exit 1; fi
[ "$(awk -F= '$1=="IMAGE_TAG"{print $2}' .env)" = "1.6.2" ] || { echo 'IMAGE_TAG must be 1.6.2'; exit 1; }
docker compose --profile monitoring config >/dev/null
echo 'cpu subagent preflight: OK'
