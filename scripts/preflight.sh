#!/bin/sh
set -eu

ENV_FILE=${ENV_FILE:-.env}
STATIC_ONLY=false
[ "${1:-}" = "--static" ] && STATIC_ONLY=true

fail() { echo "preflight: ERROR: $*" >&2; exit 1; }
info() { echo "preflight: $*"; }

[ -f docker-compose.yml ] || fail "run from the repository root"

# Production compose must never silently fall back to source builds or broad .env injection.
if grep -Eq '^[[:space:]]+build:' docker-compose.yml; then fail "production docker-compose.yml contains build:"; fi
if grep -Eq '^[[:space:]]+env_file:' docker-compose.yml; then fail "production docker-compose.yml contains env_file:"; fi
for image in neuroforge neuroforge-worker agent agent-data-init knowledge control; do
  grep -Fq "git.send.nrw/sendnrw/glpi-neuroforge-mega-${image}:\${IMAGE_TAG:" docker-compose.yml || fail "registry image mapping missing for ${image}"
done

if [ "$STATIC_ONLY" = true ]; then
  if grep -Eq '^IMAGE_TAG[[:space:]]*=[[:space:]]*latest([[:space:]]|$)' .env.example; then fail ".env.example sets IMAGE_TAG=latest"; fi
  info "static compose/source checks passed"
  exit 0
fi

[ -f "$ENV_FILE" ] || fail "$ENV_FILE not found (copy .env.example and replace every placeholder)"
getv() { sed -n "s/^$1=//p" "$ENV_FILE" | tail -n 1 | tr -d '\r'; }
check_not_placeholder() {
  name=$1; value=$(getv "$name")
  [ -n "$value" ] || fail "$name is empty"
  upper=$(printf '%s' "$value" | tr '[:lower:]' '[:upper:]')
  case "$upper" in *CHANGE_ME*|*CHANGEME*|*PLACEHOLDER*) fail "$name still contains a placeholder";; esac
}
check_secret() {
  name=$1; min=$2
  check_not_placeholder "$name"
  value=$(getv "$name")
  [ "${#value}" -ge "$min" ] || fail "$name must contain at least $min characters"
}

tag=$(getv IMAGE_TAG)
[ -n "$tag" ] || fail "IMAGE_TAG is empty"
[ "$tag" != latest ] || fail "IMAGE_TAG=latest is forbidden for production"
case "$tag" in *[!A-Za-z0-9._-]*) fail "IMAGE_TAG contains invalid characters";; esac

for spec in \
  NEUROFORGE_ADMIN_TOKEN:24 \
  NEUROFORGE_APP_API_KEY:24 \
  NEUROFORGE_INTEGRATION_TOKEN:24 \
  NEUROFORGE_CONTROL_READ_TOKEN:24 \
  NEUROFORGE_WORKER_TOKEN:24 \
  NEUROFORGE_METRICS_TOKEN:24 \
  KB_INTEGRATION_TOKEN:24 \
  CONTROL_READ_TOKEN:24 \
  BASIC_AUTH_PASSWORD:12 \
  CONTROL_BASIC_AUTH_PASSWORD:12 \
  WEB_PASSWORD:12; do
  check_secret "${spec%%:*}" "${spec##*:}"
done

for name in GLPI_URL GLPI_CLIENT_ID GLPI_CLIENT_SECRET GLPI_USERNAME GLPI_PASSWORD; do
  check_not_placeholder "$name"
done
web_anon=$(printf '%s' "$(getv WEB_ALLOW_ANONYMOUS)" | tr '[:upper:]' '[:lower:]')
if [ "$web_anon" != "true" ]; then
  check_not_placeholder WEB_USERNAME
fi
research=$(printf '%s' "$(getv NEUROFORGE_SEARXNG_ENABLED)" | tr '[:upper:]' '[:lower:]')
if [ "$research" = "true" ]; then
  check_secret SEARXNG_SECRET 24
fi

# Trust-boundary tokens must not be reused across roles.
seen=''
for name in NEUROFORGE_ADMIN_TOKEN NEUROFORGE_APP_API_KEY NEUROFORGE_INTEGRATION_TOKEN NEUROFORGE_CONTROL_READ_TOKEN NEUROFORGE_WORKER_TOKEN NEUROFORGE_METRICS_TOKEN KB_INTEGRATION_TOKEN CONTROL_READ_TOKEN; do
  value=$(getv "$name")
  case "|$seen|" in *"|$value|"*) fail "$name reuses another service token";; esac
  seen=${seen:+$seen|}$value
done

command -v docker >/dev/null 2>&1 || fail "docker is not installed"
docker compose version >/dev/null 2>&1 || fail "Docker Compose v2 is unavailable"
docker compose --env-file "$ENV_FILE" config -q || fail "docker compose config validation failed"
images=$(docker compose --env-file "$ENV_FILE" config --images)
for image in neuroforge neuroforge-worker agent agent-data-init knowledge control; do
  expected="git.send.nrw/sendnrw/glpi-neuroforge-mega-${image}:${tag}"
  printf '%s\n' "$images" | grep -Fxq "$expected" || fail "resolved image missing: $expected"
done
printf '%s\n' "$images" | grep -E 'git\.send\.nrw/sendnrw/glpi-neuroforge-mega-.*:latest$' >/dev/null && fail "a project image resolved to latest"
info "production preflight passed for IMAGE_TAG=$tag"
