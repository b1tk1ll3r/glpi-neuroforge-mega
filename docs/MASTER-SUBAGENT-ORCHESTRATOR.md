# NeuroForge Master / Subagent Orchestrator (v1.6.0)

## Zielbild

NeuroForge ist der autoritative **Master/Orchestrator**. Subagents besitzen keinen eigenen autoritativen Knowledge-State. Sie registrieren sich mit Resource-Class und Capabilities, ziehen passende Jobs und liefern Ergebnisse unter Lease-Fencing zurück.

```text
                         durable WAL/checkpoint
                                │
                       ┌────────▼────────┐
                       │ NeuroForge      │
                       │ Master          │
                       │ scheduler + DAG │
                       └───────┬─────────┘
                  capability /│\ lease-fenced jobs
                             / │ \
                ┌───────────┐  │  ┌─────────────┐
                │ CPU agent │  │  │ GPU agent   │
                │ relinking │  │  │ chat/embed  │
                └───────────┘  │  └─────────────┘
                               │
                         more subagents
```

## Konsistenzmodell

- Jobs sind persistent; Queue, Retry, Dependencies und Worker-Ergebnis überleben einen Master-Neustart.
- Jeder Claim erhält ein eindeutiges `lease_token`. Eine verspätete Antwort einer abgelaufenen Lease wird auch dann abgewiesen, wenn der Job noch nicht neu vergeben wurde; nach Reclaim verhindert der neue Token zusätzlich stale writes.
- Heartbeats verlängern nur aktive, zum Worker passende Leases.
- Fehler werden exponentiell mit begrenztem Backoff wiederholt; `max_attempts` verhindert Endlosschleifen.
- Abhängigkeiten (`depends_on`) bilden einen DAG: Child-Jobs bleiben `blocked`, bis alle Parents `done` sind. Ein terminal fehlgeschlagener Parent lässt das Child fail-closed scheitern.
- Worker-Ergebnisse, die autoritativen Master-State verändern (`vector.relink`), verwenden eine zweite persistente Phase `apply_wait`. Der Ergebnisblob ist bereits geschrieben, bevor der Master ihn anwendet. Master-Apply wird idempotent wiederholt und erst danach wird der Job `done`.
- Terminale Job-Historie wird nach Retention/Cap bereinigt; noch referenzierte Dependency-Jobs werden erhalten.
- In Clusterbetrieb plant und appliziert nur der aktuelle NeuroForge-Leader. Standalone ist der einzelne Master autoritativ.

## Resource-/Capability-Routing

Aktuelle Standardtypen:

| Job | Resource | Capabilities | Wirkung |
|---|---|---|---|
| `vector.relink` | CPU | `cpu,vector.relink` | ANN-Kandidaten exakt nachbewerten, n:m-Synapsen liefern |
| `model.embed` | GPU | `gpu,model.embed` | Embedding über den GPU-Subagent/Ollama |
| `model.chat` | GPU | `gpu,model.chat` | Chat/Structured Output über den GPU-Subagent/Ollama |

Ein Worker kann zusätzliche Capabilities deklarieren. Ein Job wird nur an einen Worker vergeben, der **alle** verlangten Capabilities besitzt und unter seinem `max_concurrency` liegt.

## Knowledge-Graph statt 1:1

Eine Graphdatenbank besteht intern weiterhin aus paarweisen Kanten. Entscheidend für einen echten Graphen ist, dass ein Knoten mehrere Kanten besitzen kann und Traversierung mehrere Hops folgt. v1.6.0 stellt beides sicher:

- `vector.relink` verbindet einen Memory-Knoten mit mehreren ANN-Nachbarn (`RecallK`).
- `GraphBackfillMinDegree` erzwingt für geeignete Memories einen Mindestgrad statt einer einzigen Partnerkante.
- `synapseAdj` hält eine echte Adjazenzliste und verhindert O(E)-Scans pro Hop.
- Retrieval propagiert bounded über `GraphMaxHops` Hops mit `GraphHopDecay`.
- Kanten tragen `relations[]`, z.B. `semantic_similarity`, `association`, `coactivation`.
- `GraphStats` misst `multi_linked_memories`, Max-/Durchschnittsgrad, Connected Components und Largest Component. Damit ist n:m-Verkettung objektiv prüfbar.

Der aktuelle automatische Backfill erzeugt primär **assoziative/semantische** Beziehungen. Das ist ein Wissensgraph im graphentheoretischen Sinn, aber noch keine vollständig ontologische Aussagenlogik wie `causes`, `solves`, `depends_on`. Solche Relationstypen können später kontrolliert ergänzt werden, ohne das n:m-/Multi-Hop-Fundament zu ändern.

## Bulk-Backfill für große Knowledge-Bestände

Der Master legt **nicht** alle möglichen Paare als O(N²)-Jobs an. Für jeden Kandidaten wird zuerst der ANN-Index verwendet; nur eine begrenzte Menge Vektoren wird in einen `vector.relink`-Job geschrieben.

Wichtige Limits:

```env
NEUROFORGE_GRAPH_BACKFILL_BATCH_SIZE=64
NEUROFORGE_GRAPH_BACKFILL_MAX_QUEUED=256
NEUROFORGE_GRAPH_BACKFILL_MIN_DEGREE=3
NEUROFORGE_GRAPH_CANDIDATE_MULTIPLIER=6
NEUROFORGE_GRAPH_RETRY_AFTER_MINUTES=360
```

Bereits wartende Targets werden bei der nächsten Planung übersprungen, damit ein großer Korpus nicht durch die lexikographisch ersten IDs verhungert.

## Monitoring

Read-only Control-API:

```text
GET /api/v1/integrations/orchestrator/status
GET /api/v1/integrations/graph/status
```

Admin-API:

```text
GET  /admin/api/orchestrator/status
GET  /admin/api/orchestrator/jobs
POST /admin/api/orchestrator/jobs/{id}/retry
POST /admin/api/orchestrator/jobs/{id}/cancel
GET  /admin/api/graph/status
POST /admin/api/graph/backfill
```

Prometheus enthält u.a.:

```text
neuroforge_jobs{status="queued|claimed|retry_wait|blocked|apply_wait|done|failed|canceled"}
neuroforge_workers{resource="cpu|gpu",status="online|stale"}
neuroforge_worker_inflight{worker="...",resource="..."}
neuroforge_worker_capacity{worker="...",resource="..."}
neuroforge_graph_memories{state="linked|isolated|multi_linked"}
neuroforge_graph_max_degree
neuroforge_graph_average_degree
neuroforge_graph_connected_components
neuroforge_graph_largest_component
```

## Remote Subagent

Auf einem zusätzlichen Host nur `docker-compose.subagent.yml` und eine kleine `.env` bereitstellen. Beispiel GPU:

```env
IMAGE_TAG=1.6.0
NEUROFORGE_MASTER_URL=https://neuroforge.internal.example
NEUROFORGE_WORKER_TOKEN=<shared-worker-token>
NEUROFORGE_GPU_WORKER_ID=gpu-node-02
NEUROFORGE_WORKER_OLLAMA_URL=http://127.0.0.1:11434
OLLAMA_MODEL=gemma4
OLLAMA_EMBEDDING_MODEL=embeddinggemma
NEUROFORGE_GPU_WORKER_CONCURRENCY=1
```

Dann:

```bash
docker compose -f docker-compose.subagent.yml --profile gpu up -d
```

Der Master-Port muss vom Subagent erreichbar sein. Für Netze außerhalb eines vertrauenswürdigen internen Segments gehört TLS/mTLS vor den Master-Endpunkt (Reverse Proxy/Service Mesh). Der Worker-Token ist ein Service-Credential und muss separat von App/Admin/Integration-Tokens bleiben.

## Operationaler Zielzustand

Ein gesunder Zustand hat:

- mindestens einen `online` CPU-Worker für Graph-Konvergenz,
- optional mindestens einen `online` GPU-Worker für Offload,
- keine dauerhaft wachsende `failed`-/`apply_wait`-Queue,
- sinkende Zahl `isolated` und steigende Zahl `multi_linked`,
- `max_degree > 1` und `largest_component > 1`,
- bounded Queue und stabile Worker-Leases,
- keine Readiness-Abhängigkeit des Masters vom Worker (verhindert Startup-Deadlocks).
