# Distributed Deployment — v1.6.2

The distributed deployment is part of the canonical release, not a separate code fork.

- `deployments/master`: authoritative NeuroForge Master, GLPI Agent, Knowledge, Control, optional Research, Prometheus and Grafana.
- `deployments/cpu-subagent`: remote CPU execution node for graph/relink work.
- `deployments/gpu-subagent`: remote GPU execution node plus Ollama for chat/embedding work.

All three roles ship with complete `.env` templates containing `CHANGE_ME_...` placeholders. The same real `NEUROFORGE_WORKER_TOKEN` must be configured on Master and both worker roles.

Recommended startup order: GPU subagent, CPU subagent, then Master. Keep the NeuroForge data volume authoritative on the Master only.

The v1.6.1 recovery/OOM fixes are included unchanged in v1.6.2. Do not delete the existing `neuroforge-data` volume when upgrading.
