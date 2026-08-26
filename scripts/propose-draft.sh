#!/bin/sh
set -eu
: "${KB_INTEGRATION_TOKEN:?set KB_INTEGRATION_TOKEN}"
KB_URL=${KB_URL:-http://127.0.0.1:${KNOWLEDGE_HOST_PORT:-8081}}
if [ "$#" -ne 1 ]; then
  echo "usage: $0 proposal.json" >&2
  exit 2
fi
curl --fail-with-body -sS \
  -H "Authorization: Bearer $KB_INTEGRATION_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary "@$1" \
  "$KB_URL/api/integrations/staging"
printf '\n'
