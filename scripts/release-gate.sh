#!/bin/sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$ROOT"
./scripts/preflight.sh --static
./scripts/check-compose-env.py
./scripts/secret-scan.sh
for module in platform/neuroforge services/agent services/knowledge services/control; do
  echo "release-gate: $module: test"
  (cd "$module" && go test ./...)
  echo "release-gate: $module: vet"
  (cd "$module" && go vet ./...)
  echo "release-gate: $module: build"
  (cd "$module" && go build ./...)
done

echo "release-gate: targeted race checks"
(cd platform/neuroforge && go test -race ./internal/store ./internal/brain ./internal/httpapi)
(cd services/agent && go test -race ./internal/state ./internal/knowledge ./internal/learning ./internal/agent)
(cd services/knowledge && go test -race ./internal/store ./internal/staging ./cmd/server)
(cd services/control && go test -race .)

echo "release-gate: passed local source gates"
