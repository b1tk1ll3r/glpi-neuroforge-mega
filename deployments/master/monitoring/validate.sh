#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")"
command -v docker >/dev/null 2>&1 || { echo "FEHLT: docker"; exit 1; }
ENV_FILE=../.env
getenv() { grep -E "^$1=" "$ENV_FILE" | head -1 | cut -d= -f2-; }
CPU_TARGET=$(getenv CPU_SUBAGENT_NODE_EXPORTER_TARGET)
GPU_TARGET=$(getenv GPU_SUBAGENT_NODE_EXPORTER_TARGET)
GPU_DCGM_TARGET=$(getenv GPU_SUBAGENT_DCGM_EXPORTER_TARGET)
METRICS_TOKEN=$(getenv NEUROFORGE_METRICS_TOKEN)
PROM_IMAGE=$(getenv PROMETHEUS_IMAGE)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
sed -e "s|__CPU_TARGET__|${CPU_TARGET}|g" \
    -e "s|__GPU_TARGET__|${GPU_TARGET}|g" \
    -e "s|__GPU_DCGM_TARGET__|${GPU_DCGM_TARGET}|g" \
    prometheus/prometheus.yml.template > "$TMP/prometheus.yml"
printf '%s' "$METRICS_TOKEN" > "$TMP/neuroforge_metrics.token"
docker run --rm --entrypoint=/bin/promtool \
  -v "$TMP/prometheus.yml:/etc/prometheus/prometheus.yml:ro" \
  -v "$PWD/prometheus/alerts.yml:/etc/prometheus-alerts/alerts.yml:ro" \
  -v "$TMP/neuroforge_metrics.token:/etc/prometheus-secrets/neuroforge_metrics.token:ro" \
  "$PROM_IMAGE" check config /etc/prometheus/prometheus.yml
echo "Prometheus promtool validation: OK"
