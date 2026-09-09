# GLPI NeuroForge Mega v1.6.2

## Consolidated production release

v1.6.2 is the canonical full repository release. It keeps the v1.6.1 runtime/data format and recovery/OOM hotfixes, and consolidates all previously split deployment variants into one coherent tree.

### Included deployment modes

- Full Mega stack with local CPU/GPU workers.
- Distributed Master with remote CPU and GPU subagents.
- Standalone GLPI Agent using a local vector backend.
- Standalone Knowledge service.
- Standalone Ollama.
- Combined Agent + Knowledge + Ollama core stack without NeuroForge.
- Prometheus/Grafana example monitoring on the distributed Master, including payload/queue/graph metrics and alerts.

### Packaging / CI corrections

- Restores the complete v1.6.1 NeuroForge recovery hardening and graph/orchestrator source tree.
- Includes complete `.env` files with safe placeholders for Master, CPU subagent, GPU subagent and standalone roles.
- Removes embedded real credentials from deployment examples.
- Replaces conflicting release workflows with one root CI workflow and one immutable tag release workflow.
- CI covers NeuroForge, Agent, Knowledge and Control plus all six project images.
- `docker-bake.hcl` builds NeuroForge server/worker, Agent, Agent data-init, Knowledge and Control.
- Removes nested/legacy workflow copies and stale duplicate compose files.
- Regenerates a single repository manifest for the exact final archive.

### Runtime safety inherited from v1.6.1

- Streaming compaction of legacy terminal `vector.relink` payloads before normal state loading.
- Completed relink payload/result blobs are discarded after successful Master apply and during WAL recovery.
- Bounded pending durable job payload bytes.
- HNSW delta checkpoints avoid full graph/vector deep copies.
- Index-first checkpoint ordering and fail-closed authoritative JSON recovery.
- `/livez` and a bootstrap `/admin` page are available while store recovery is still running.
- Conservative graph backfill and job-retention defaults for large Knowledge corpora.

There is no intentional persisted-data-format break from v1.6.1 to v1.6.2.
