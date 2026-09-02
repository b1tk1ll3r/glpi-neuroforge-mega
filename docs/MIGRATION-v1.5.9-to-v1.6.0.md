# Migration v1.5.9 → v1.6.0

Die Migration ist **nicht destruktiv**. Keine Volumes, Knowledge-Dateien, Memories, Goals oder Staging-Drafts löschen.

1. v1.6.0 Images bauen/publizieren.
2. `IMAGE_TAG=1.6.0` setzen.
3. Die neuen Orchestrator-/Graph-Variablen aus `.env.example` übernehmen. Die Defaults sind produktionsnah und bounded.
4. Den bisherigen einzelnen `neuroforge-worker` durch `neuroforge-worker-cpu` und `neuroforge-worker-gpu` ersetzen.
5. Stack neu erzeugen; Volumes beibehalten.

```bash
docker compose --profile research pull
docker compose --profile research up -d --force-recreate --remove-orphans
```

Danach prüfen:

```bash
docker compose ps
curl -s -H "Authorization: Bearer $NEUROFORGE_CONTROL_READ_TOKEN" \
  http://127.0.0.1:${NEUROFORGE_HOST_PORT:-8090}/api/v1/integrations/orchestrator/status | jq
curl -s -H "Authorization: Bearer $NEUROFORGE_CONTROL_READ_TOKEN" \
  http://127.0.0.1:${NEUROFORGE_HOST_PORT:-8090}/api/v1/integrations/graph/status | jq
```

Ein gesunder Zustand hat mindestens einen online CPU-Worker, keine dauerhaft wachsende `failed`/`apply_wait` Queue und eine sinkende Zahl isolierter Memories. GPU-Offload ist optional; ohne online GPU-Subagent fällt NeuroForge auf den normalen Provider-Router zurück.

Remote Subagents erhalten eindeutige `NEUROFORGE_CPU_WORKER_ID` bzw. `NEUROFORGE_GPU_WORKER_ID`. Außerhalb eines vertrauenswürdigen internen Netzes den Master nur hinter TLS/mTLS veröffentlichen.
