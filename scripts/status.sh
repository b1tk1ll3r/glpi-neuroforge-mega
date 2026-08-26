#!/bin/sh
set -eu
PORT="${CONTROL_HOST_PORT:-8070}"
curl -fsS "http://127.0.0.1:${PORT}/api/status" | python3 -m json.tool
