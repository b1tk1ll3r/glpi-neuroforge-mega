#!/bin/sh
set -eu
ENV_FILE=${ENV_FILE:-.env}
getv() {
  [ -f "$ENV_FILE" ] || return 0
  sed -n "s/^$1=//p" "$ENV_FILE" | tail -n1 | tr -d '\r'
}
PORT=${CONTROL_HOST_PORT:-$(getv CONTROL_HOST_PORT)}
PORT=${PORT:-8070}
USER_NAME=${CONTROL_BASIC_AUTH_USER:-$(getv CONTROL_BASIC_AUTH_USER)}
PASSWORD=${CONTROL_BASIC_AUTH_PASSWORD:-$(getv CONTROL_BASIC_AUTH_PASSWORD)}
if [ -n "$USER_NAME" ] || [ -n "$PASSWORD" ]; then
  [ -n "$USER_NAME" ] && [ -n "$PASSWORD" ] || { echo "status: incomplete Control Basic Auth" >&2; exit 1; }
  curl -fsS --user "$USER_NAME:$PASSWORD" "http://127.0.0.1:${PORT}/api/status" | python3 -m json.tool
else
  curl -fsS "http://127.0.0.1:${PORT}/api/status" | python3 -m json.tool
fi
