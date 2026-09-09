# GLPI NeuroForge Mega v1.6.2

v1.6.2 is the consolidated production release that reunifies the full Mega repository after the stripped-core packaging experiment and adds authenticated Ollama-compatible endpoints.

## Complete deployment set

The repository now ships all supported operating modes together:

- full single-host Mega stack,
- distributed Master,
- remote CPU subagent,
- remote GPU subagent with Ollama,
- standalone GLPI Agent,
- standalone Knowledge service,
- standalone Ollama,
- combined Agent + Knowledge + Ollama core,
- Prometheus/Grafana example monitoring on the Master.

The v1.6.1 recovery/OOM hardening remains included, including startup pre-compaction of legacy relink payloads, bounded durable-job payload retention, lower graph-backfill pressure, visible startup phases, stricter corrupted-state handling and HNSW checkpoint memory reductions.

## Ollama Bearer authentication

A shared optional variable is now supported:

```env
OLLAMA_API_KEY=
```

When non-empty, clients send:

```http
Authorization: Bearer <OLLAMA_API_KEY>
```

for Ollama-compatible `/api/tags`, `/api/chat` and `/api/embed` requests.

Supported clients:

- GLPI Agent, including every node in its Ollama pool,
- Knowledge AI fallback,
- NeuroForge provider and readiness/provider-health checks,
- NeuroForge GPU/model subagents.

NeuroForge also supports role-specific overrides:

```env
NEUROFORGE_OLLAMA_API_KEY=
NEUROFORGE_WORKER_OLLAMA_API_KEY=
```

The role-specific value takes precedence over `OLLAMA_API_KEY` for the corresponding NeuroForge component.

The key is stored as a NeuroForge secret when configured through `NEUROFORGE_OLLAMA_API_KEY`; admin APIs expose only configured/masked status unless secret reveal is explicitly enabled.

Native Ollama does not itself become authenticated merely by setting this variable. The option is intended for an authenticated Ollama-compatible gateway/reverse proxy or another Ollama-compatible endpoint that validates Bearer tokens. Direct native Ollama deployments should normally leave it empty.

## CI/release consolidation

Gitea workflows now live only at repository root and cover all four Go modules. The release workflow builds and pushes the six immutable project images. `docker-bake.hcl` contains the same complete image set. Production compose files remain pinned through `IMAGE_TAG`; no `latest` project image is required.
