# Distributed Deployment Kits (v1.6.0)

This directory contains three independent deployment kits:

- `master/` - authoritative NeuroForge Master plus Agent, Knowledge, Control, optional SearXNG and optional Prometheus/Grafana.
- `cpu-subagent/` - CPU worker for `vector.relink` / graph convergence.
- `gpu-subagent/` - GPU worker plus local Ollama for `model.chat` and `model.embed`.

The generated `NEUROFORGE_WORKER_TOKEN` is identical in all three `.env` files. Replace the RFC 5737 example IP addresses (`192.0.2.x`) with real reachable addresses before starting.

Recommended order:
1. GPU subagent: `docker compose --profile monitoring up -d`
2. CPU subagent: `docker compose --profile monitoring up -d`
3. Master: fill GLPI credentials, then `docker compose --profile research --profile monitoring up -d`

Only the Master holds authoritative NeuroForge state. Workers are disposable execution nodes.
