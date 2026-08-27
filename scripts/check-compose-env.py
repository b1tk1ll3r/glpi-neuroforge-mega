#!/usr/bin/env python3
"""Fail if production Compose broadens Agent secrets or misses runtime env knobs."""
from __future__ import annotations
import re
import sys
from pathlib import Path

try:
    import yaml
except Exception as exc:  # PyYAML is intentionally only a release-dev dependency.
    print(f"compose-env-check: PyYAML unavailable: {exc}", file=sys.stderr)
    raise SystemExit(2)

root = Path(__file__).resolve().parents[1]
compose = yaml.safe_load((root / "docker-compose.yml").read_text())
agent = compose["services"]["agent"]
if "env_file" in agent:
    raise SystemExit("compose-env-check: production Agent must not use env_file")
actual = set((agent.get("environment") or {}).keys())

# Canonical host-config variables are those documented in .env.example and
# literally referenced by non-test Agent runtime source. Internal service values
# are added separately below.
documented: set[str] = set()
for line in (root / ".env.example").read_text().splitlines():
    match = re.match(r"([A-Z][A-Z0-9_]+)=", line)
    if match:
        documented.add(match.group(1))
source = "\n".join(
    p.read_text(errors="ignore")
    for p in (root / "services/agent").rglob("*.go")
    if not p.name.endswith("_test.go")
)
expected = {name for name in documented if re.search(rf'"{re.escape(name)}"', source)}
expected.update({"NEUROFORGE_URL", "NEUROFORGE_API_KEY", "BRAIN_ACTIVITY_URL", "BRAIN_ACTIVITY_API_KEY"})
missing = sorted(expected - actual)
if missing:
    print("compose-env-check: Agent runtime variables missing from explicit environment:", file=sys.stderr)
    print("\n".join(f"  {name}" for name in missing), file=sys.stderr)
    raise SystemExit(1)

forbidden = {
    "NEUROFORGE_ADMIN_TOKEN",
    "NEUROFORGE_WORKER_TOKEN",
    "NEUROFORGE_METRICS_TOKEN",
    "NEUROFORGE_CLUSTER_TOKEN",
    "NEUROFORGE_CONTROL_READ_TOKEN",
    "KB_INTEGRATION_TOKEN",
    "BASIC_AUTH_PASSWORD",
    "CONTROL_BASIC_AUTH_PASSWORD",
}
leaked = sorted(forbidden & actual)
if leaked:
    print("compose-env-check: unrelated privileged secrets exposed to Agent: " + ", ".join(leaked), file=sys.stderr)
    raise SystemExit(1)

knowledge = compose["services"]["knowledge"]
if "env_file" in knowledge:
    raise SystemExit("compose-env-check: production Knowledge must not use env_file")
print(f"compose-env-check: passed ({len(actual)} explicit Agent environment entries)")
