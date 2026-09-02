# GLPI NeuroForge Mega v1.6.0

v1.6.0 erweitert NeuroForge von einem einzelnen Workerpfad zu einem dauerhaften Master/Subagent-Orchestrator und schließt die Graph-Lücke großer Knowledge-Imports.

## Orchestrator

- persistente Jobs im NeuroForge-WAL/Checkpoint
- Resource-Class + Capability-Routing (`cpu`, `gpu`, `vector.relink`, `model.chat`, `model.embed`)
- Priorität, Idempotency-Key, Parent/Dependencies, Retry mit begrenztem Backoff und Max-Attempts
- Heartbeats, Worker-Staleness, Max-Concurrency und lease-token fencing
- abgelaufene Leases werden auch dann abgewiesen, wenn noch kein anderer Worker den Job übernommen hat
- Worker-Ergebnisse mit Master-Mutation verwenden `apply_wait`: Resultat zuerst persistent, danach idempotenter Master-Apply
- Apply-Retries überleben Neustarts; terminale Jobhistorie wird bounded bereinigt
- Admin-Status, Jobliste, Retry, Cancel und manueller Graph-Backfill

## Knowledge Graph

- importierte `knowledge.chunk`-Memories werden bounded nachverknüpft
- ANN bestimmt nur Kandidaten; CPU-Subagents führen exakte Similarity-Bewertung aus
- ein Memory kann mehrere Edges besitzen; `GraphBackfillMinDegree` verhindert den früher praktisch isolierten/1:1-artigen Zustand
- Adjazenzindex + bounded Multi-Hop-Retrieval mit Hop-Decay
- Synapsen können mehrere `relations[]` tragen
- Graphmetriken: linked/isolated/multi-linked, average/max degree, connected components, largest component
- Relink-Payload bindet Target und Kandidaten an Version + Vector-Fingerprint; veraltete Delete/Recreate-Ergebnisse werden verworfen

## Deployment

Der lokale Stack enthält getrennte CPU- und GPU-Subagents. Zusätzliche Hosts verwenden `docker-compose.subagent.yml`. Für Remote-Netze ist TLS/mTLS vor dem Master-Endpunkt erforderlich; der Worker-Token bleibt ein separates Service-Credential.

## Compatibility

Die Migration ist nicht destruktiv. Bestehende Memories/Knowledge/Goals bleiben erhalten. Nach dem Upgrade beginnt der Graph-Backfill kontrolliert im Hintergrund; deshalb können `neuroforge_synapses` und die Graph-Connectivity-Metriken nach und nach steigen.
