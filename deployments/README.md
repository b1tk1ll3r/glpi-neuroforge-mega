# Deployment Kits — GLPI NeuroForge Mega v1.6.2

This repository intentionally supports both the complete NeuroForge platform and stripped standalone operation.

## Complete / distributed platform

- `master/` — authoritative NeuroForge Master plus GLPI Agent, Knowledge, Control, optional SearXNG, Prometheus and Grafana. Remote CPU/GPU workers connect to this node.
- `cpu-subagent/` — disposable CPU worker with `cpu,vector.relink` capabilities.
- `gpu-subagent/` — disposable GPU worker plus Ollama with `gpu,model.chat,model.embed` capabilities.

The Master is the only authoritative owner of NeuroForge state. CPU/GPU workers use leases, heartbeats and fenced job completion.

## Standalone core operation

- `agent/` — GLPI Agent with local Knowledge vector backend; no NeuroForge, Control or Research dependency.
- `knowledge/` — standalone Knowledge editor/service.
- `ollama/` — standalone Ollama runtime.
- `combined/` — Agent + Knowledge + Ollama on one host, still without NeuroForge.

The standalone Agent and Knowledge kits share `runtime/knowledge` by default. The Agent mounts it read-only; Knowledge mounts it read-write.

## Release rules

- Project images are pinned by `IMAGE_TAG=1.6.2`; production compose files do not require `latest`.
- Replace every `CHANGE_ME_...` placeholder before startup.
- Never use `docker compose down -v` during an in-place upgrade unless loss of persistent state is intended.
- For a v1.6.0/v1.6.1 NeuroForge data volume, keep the volume: v1.6.2 includes the v1.6.1 recovery/OOM hotfixes and startup compaction path.

See each role's README/preflight and the root `README.md` for startup order.
