# GLPI NeuroForge Mega v1.5.0

v1.5.0 is a Stop-the-Line hardening release. It closes deployment, secret-isolation, authorization, readiness, staging-consistency and concurrent-goal gaps found during the v1.4.5 end-to-end review.

## Security and trust boundaries

- NeuroForge now has separate App, Integration, Control-Read, Worker, Metrics, Admin and optional Cluster credentials.
- Integration writes (`knowledge`, events, validated outcomes) accept only the Integration token or Admin token.
- Control-Read can read NeuroForge stats/graphs but cannot call `/learn` or integration writes.
- Environment-managed NeuroForge secrets cannot be pseudo-rotated through the Admin UI and silently revert after restart.
- Placeholder/short critical secrets are rejected by runtime validation and production preflight.
- Control Center has its own Basic Auth; `/healthz` stays public for container monitoring.
- Knowledge browser writes are protected by same-origin / `Sec-Fetch-Site` checks.
- Production Compose no longer uses `env_file`; Agent and Knowledge receive explicitly scoped environment variables only.

## Research, goals and Knowledge staging

- Research → progress → human-review staging no longer depends on semantic goal-summary learning.
- Draft evidence considers up to 200 Research runs and durable goal provenance.
- NeuroForge checks the Knowledge staging bridge through an authenticated health endpoint.
- Goal cycles are single-flight per Goal; a parallel duplicate receives `409 Conflict`.
- Promotion is transactional: if staging archival fails after production import, the new production article is rolled back.
- Atomic Knowledge/Staging namespace updates use directory sync and hardened rollback handling.

## Runtime and deployment

- Production `docker-compose.yml` is registry-only and requires immutable `${IMAGE_TAG}` for all six project images.
- `docker-compose.dev.yml` is the explicit local-source-build override.
- Worker token is environment-only and no longer appears in process arguments.
- Agent has a distroless-compatible binary healthcheck.
- NeuroForge readiness can live-check Ollama `/api/tags` and requires both configured chat and embedding models; Mega enables this by default.
- Knowledge and Control have graceful SIGTERM shutdown; Compose grace periods are aligned.
- Knowledge and Control no longer depend on NeuroForge availability; Agent no longer waits for NeuroForge health before starting.
- Agent's legacy Knowledge editor is disabled by default in Mega; `AGENT_LEGACY_KNOWLEDGE_EDITOR_ENABLED=true` is the explicit compatibility escape hatch.
- `AI_CONTENT_LABEL_ENABLED` is now actually loaded by the Agent configuration.
- All four project Dockerfiles run `go test ./...` and `go vet ./...` before producing runtime binaries.

## Release operations

- `scripts/preflight.sh`: no-`make` production validation, immutable image tag and secret checks.
- `scripts/go-live.sh`: preflight → registry pull → compose up.
- `scripts/backup-data.sh` / `scripts/restore-data.sh`: NeuroForge, Agent and Knowledge data backup/restore with SHA-256 manifest.
- `scripts/secret-scan.sh` and `scripts/release-gate.sh`: source security and Go test/vet/build/race gates.
- Host smoke procedure: `docs/GO-LIVE-v1.5.0.md`.

## Required operator changes from v1.4.5

Create new unique values for `NEUROFORGE_INTEGRATION_TOKEN`, `NEUROFORGE_CONTROL_READ_TOKEN`, `CONTROL_BASIC_AUTH_USER` and `CONTROL_BASIC_AUTH_PASSWORD`; set a non-`latest` `IMAGE_TAG`; then run `./scripts/preflight.sh` before pulling/updating containers. See `docs/MIGRATION-v1.4.5-to-v1.5.0.md`.

## External release gate

This review environment has Go but no Docker daemon and no real GLPI/SearXNG/Ollama production services. Source, race, static deployment, patch and package checks are performed locally; the real registry pull/container/GLPI smoke test remains mandatory on the target host and is not claimed as locally passed.
