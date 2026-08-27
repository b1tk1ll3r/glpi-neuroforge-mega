# Validation – v1.5.0

This file records reproducible release gates. It deliberately distinguishes checks possible in the source-review environment from host-only Docker/GLPI checks.

## Locally executed

- Four Go modules: `go test ./...` — PASS.
- Four Go modules: `go vet ./...` — PASS.
- Four Go modules: `go build ./...` — PASS.
- Targeted race suites: NeuroForge Store/Brain/HTTP API, Agent state/knowledge/learning/core agent, Knowledge Store/Staging/Server, Control — PASS.
- NeuroForge live Ollama readiness regression: missing embedding model → 503; both configured models → 200 — PASS.
- Goal per-ID single-flight regression — PASS.
- Scoped-token trust-boundary regression — PASS.
- Environment-managed Admin-secret mutation regression — PASS.
- Knowledge browser same-origin regression — PASS.
- Knowledge promotion rollback regression — PASS.
- Control Center Basic Auth regression — PASS.
- Production Compose static gate: no project `build:`, no `env_file:`, exact six registry image families with `${IMAGE_TAG}` — PASS.
- Agent production environment secret-isolation check — PASS.
- Shell syntax (`sh -n`) and `git diff --check` — PASS.
- Engineering graph regeneration/idempotence — PASS.

## Host-only, still mandatory

The review environment does not provide Docker/Compose, GLPI, SearXNG or a production Ollama. Therefore the following are release-host gates rather than local PASS claims:

- authenticated registry pull of the final immutable tag;
- `docker compose config`, `pull`, `up` and health state using the real `.env`;
- real Ollama model inventory and inference;
- GLPI read/write smoke flow;
- optional SearXNG research flow;
- backup/restore rehearsal against actual Docker volumes and host paths.

Use `scripts/preflight.sh`, `scripts/go-live.sh` and `docs/GO-LIVE-v1.5.0.md` on the target host.
