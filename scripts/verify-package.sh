#!/usr/bin/env sh
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

for f in \
  services/agent/Dockerfile \
  services/knowledge/Dockerfile \
  .github/workflows/ci.yml \
  .github/workflows/release.yml \
  .github/dependabot.yml \
  docker-bake.hcl; do
  test -f "$f" || { echo "missing: $f" >&2; exit 1; }
done

grep -q '^FROM ' services/agent/Dockerfile
grep -q 'AS data-init' services/agent/Dockerfile
grep -q '^FROM ' services/knowledge/Dockerfile

echo "OK: Dockerfiles and repository workflows are present."
