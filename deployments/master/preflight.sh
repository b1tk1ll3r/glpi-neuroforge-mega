#!/usr/bin/env sh
set -eu
[ -f .env ] || { echo 'missing .env'; exit 1; }
required='IMAGE_TAG NEUROFORGE_ADMIN_TOKEN NEUROFORGE_APP_API_KEY NEUROFORGE_INTEGRATION_TOKEN NEUROFORGE_CONTROL_READ_TOKEN NEUROFORGE_WORKER_TOKEN NEUROFORGE_METRICS_TOKEN KB_INTEGRATION_TOKEN CONTROL_READ_TOKEN GLPI_URL GLPI_CLIENT_ID GLPI_CLIENT_SECRET GLPI_USERNAME GLPI_PASSWORD GRAFANA_ADMIN_PASSWORD'
for k in $required; do
  v=$(awk -F= -v key="$k" '$1==key {sub(/^[^=]*=/,""); print; exit}' .env)
  [ -n "$v" ] || { echo "missing: $k"; exit 1; }
done
if grep -Eq '(^|=)(https?://)?192\.0\.2\.|example\.invalid|CHANGE_ME|YOUR-' .env; then
  echo 'placeholder/example values remain in .env'; exit 1
fi
[ "$(awk -F= '$1=="IMAGE_TAG"{print $2}' .env)" = "1.6.2" ] || { echo 'IMAGE_TAG must be 1.6.2'; exit 1; }
docker compose --profile research --profile monitoring config >/dev/null
echo 'master preflight: OK'
