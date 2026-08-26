# Changelog

## v0.8.2

- Per-goal **Live Research** dashboard with incremental event polling and responsive six-lane visualization for queries, search results, downloads/sources, claim/evidence candidates, duplicate/corroboration decisions, and rejected/error sources.
- Persisted bounded `ResearchRun` audit model with sequenced `ResearchEvent` records, run statistics, latest-run delta API and bounded history API.
- Goal learning cycles now expose `research_run_id`, tying the final Observe → Predict → Evaluate → Learn record back to the exact research trace.
- Research tracing is wired into SearXNG search, page/document fetch, extraction/chunking, policy skips, embedding failures, duplicate detection, independent corroboration and evidence learning.
- Operational trace events are kept live in memory and persisted once when the run finishes, avoiding a WAL fsync for every URL/chunk. Authoritative source/memory durability is unchanged.
- Interrupted running traces are marked `interrupted` on restart rather than pretending the research run completed.
- Regression coverage for trace generation, stats, persisted run ID and incremental live API deltas.


## v0.8.1

- Goal controls: pause/resume/delete are now first-class API and dashboard actions. Pausing clears the next scheduler deadline; resuming schedules an active auto-goal immediately. Deleting a goal does not delete knowledge already learned from it.
- SearXNG file results now support document ingestion. File metadata (`filename`, `mimetype`, `template`, `size`) is parsed when present, while the final HTTP Content-Type/Content-Disposition and URL extension provide fallback detection.
- Research-fetched PDF, DOCX, TXT, Markdown, CSV/TSV, JSON and YAML resources are routed through the normal document extraction/chunking/embedding pipeline with source URI and provenance.
- Research result UI identifies document hits and reports how many documents were ingested.
- Added regression/integration coverage for goal pause/resume/delete and SearXNG DOCX ingestion.

## v0.8.0

- komplett neu gestaltetes responsive Admin-UI in CSS + Vanilla JS
- Canvas Knowledge Graph mit Zoom/Pan und drei LOD-Stufen
- source-grounded Text-/Dokument-Ingestion inklusive Chunking, Deduplication und Provenance
- TXT/Markdown/HTML/JSON/CSV/TSV/DOCX sowie PDF via optionalem `pdftotext`
- persistente Knowledge Sources und optional gespeicherte Originaldateien
- SearXNG JSON Search API als Research-Backend
- optionales Abrufen von Ergebniswebseiten mit Byte-/Zeichen-/Redirect-/Timeout-Limits
- SSRF-Schutz inkl. Validierung der tatsächlich gedialten IP gegen DNS-Rebinding
- autonomer Research-Pfad für Goals vor Recall/Predict/Evaluate/Learn
- per-Goal Scheduler mit sofortigem Start, Intervall, Next-Run und Error-Backoff
- Source-Trust für ingest/document/web evidence
- Prompt-Injection-Härtung: Recall/Source-Inhalte werden explizit als untrusted data behandelt
- neue REST/Admin-Endpunkte für Ingestion, Quellen und Research
- HTTP-Body-Default 32 MiB; Docker Server enthält poppler-utils

# NeuroForge v0.7.3

## Long-running Ollama inference

- Removes the hard-coded 120 second outbound provider client timeout.
- Ollama nodes gain `request_timeout_seconds`; `0` means no model-inference deadline.
- Keeps a 10 second TCP connect timeout and a 5 second explicit provider-health timeout so unreachable hosts do not hang forever.
- Ollama chat now sends `num_ctx`, `num_predict`, `think`, and `chat_keep_alive`.
- Ollama embeddings send `embedding_keep_alive`.
- `num_predict: 0` inherits NeuroForge's caller/global output limit instead of leaving Ollama generation unbounded.
- `http.write_timeout_seconds: 0` is now valid and is the default for new installs, preventing the frontend response deadline from killing long inference.
- Admin "Modelle & Routing" exposes all new Ollama runtime controls.
- Existing data/config remain compatible; old Ollama nodes are defaulted to `think=off`, `chat_keep_alive=30m`, `embedding_keep_alive=5m`.

# NeuroForge v0.7.2

## Admin Chat Input Hotfix

- Fixes `input is required` in the Admin Dashboard even when the chat textarea contains text.
- Root cause: the textarea used `id="prompt"`, which collides with the browser built-in `window.prompt()` function.
- Chat now uses `id="chatPrompt"` and resolves all chat form elements explicitly with `document.getElementById(...)`.
- Empty chat input is rejected in the browser with a clear message before an API request is sent.
- Regression test prevents reintroducing `id="prompt"` or `prompt.value` in the chat request path.

# NeuroForge v0.7.1

## Browser/Auth Hotfix

- Fixes the Admin Dashboard chat/search/learn/goals failure `String contains non ISO-8859-1 code point`.
- Root cause: production secret masking uses Unicode bullets; the dashboard incorrectly reused the masked App API key as an `Authorization` header.
- Admin Dashboard no longer reads or reveals the App API key for its own API calls.
- Application endpoints accept a valid `X-Admin-Token` as a privileged alternative to the normal external Bearer App API key.
- External applications continue to use `Authorization: Bearer <APP_API_KEY>`.
- Browser validates the Admin token before `fetch()` and reports a clear error for whitespace/control/Unicode characters.
- App API key stays masked in production without breaking Dashboard chat/search/learn/goals.

# NeuroForge v0.7.0

## Explainability
- Knowledge Explorer mit Memory-Typen, Status, Quellen, Graph, Detailansicht und Timeline.
- Persistente Knowledge Events für Lernen, Rewards, Feedback, Konsolidierung, Goals, Konflikte und Admin-Aktionen.
- Provenance pro neuem Memory: Quelle/Actor, Embedding- und Generation-Provider/Model/Node sowie Goal/Parent-Referenzen.
- Explainable Recall mit BaseScore, GraphBoost, TypeWeight, SalienceFactor, ConfidenceFactor und CandidateSource.

## Learning Policy
- getrennte Lernschalter für Chat-Input, Chat-Response, `/learn`, Imports und Goal-Cycles.
- Source-Trust → Confidence.
- Duplicate-Suppression.
- Mindestbestätigungen/-Confidence für semantische Konsolidierung.
- maximale Memory-Textlänge.
- optionales Archivieren stark negativ bewerteter Assistant-Antworten.
- Admin API + UI.

## Production hardening
- `/livez`, `/readyz`, `/version`.
- graceful shutdown und finaler Checkpoint.
- HTTP timeouts, Header-/Body-Limits und globales Concurrency-Limit.
- constant-time Tokenvergleiche.
- Security Header/CSP.
- Admin-Secrets standardmäßig maskiert; kein Admin-Token im Startlog.
- non-root/read-only Docker-Defaults.
- Prometheus Alert-Beispiele und Production Guide.

## Compatibility
- ältere Memories bleiben lesbar; fehlende Provenance wird nicht erfunden.
- bestehende Model-Routing-, WAL-, Segment-, HNSW- und Disk-PQ-Pfade bleiben erhalten.
# Changelog

## v0.6.0-dev model routing

- neue Admin-Seite **Modelle & Routing** statt ausschließlich rohem Config-JSON
- `GET/PUT /admin/api/model-routing` für partielle Routing-/Ollama-Updates
- die einfache `routing` + `ollama` JSON-Struktur kann direkt als Teil-Update verwendet werden
- optionale Modell-/Node-Bindung für Chat/Actor und Embeddings
- neue Rollen `critic`, `consolidator` und `goal` mit Provider, Modell und optionalem strict Ollama-Node-Pinning
- Critic steuert LLM-Auto-Reward, Consolidator die LLM-Wissensverdichtung und Goal die LLM-Goal-Cycles
- ungebundene Ollama-Routen behalten gewichtetes Multi-Node-Failover
- explizit gepinnte Ollama-Rollen fallen nicht still auf einen anderen Node zurück
- Provider-Health zeigt die über Ollama `/api/tags` sichtbaren Modellnamen
- OpenAPI-Schemas für `RoutingConfig`, `OllamaServer`, `ModelRoute` und `ModelRoutingSettings`

## v0.6.0-dev observability

- explizites Admin-Dashboard unter `/admin` plus bestehender Root-UI
- neue Observability-Ansicht mit Heap/Goroutine/GC-, HTTP-, Tiering-, Cache-, Index- und Cluster-KPIs
- dependency-freie Browser-Charts und Top-Route-Tabelle
- authentifizierter Prometheus-Endpunkt `GET /metrics` im Textformat 0.0.4
- eigener automatisch erzeugter `metrics_token` plus `NEUROFORGE_METRICS_TOKEN`
- HTTP Counter/Histogramme verwenden normalisierte ServeMux-Routenmuster statt dynamischer IDs
- O(1)-Memory-Observability-Snapshot: kein Vollscan über alle Memories pro Dashboard-Refresh/Scrape
- periodisches Dashboard löst keine externen Ollama-Healthchecks mehr aus; Healthcheck bleibt manuell
- Prometheus exportiert keine Prompt-/Memory-/Session-Inhalte als Labels

## v0.5.1

Performance-Patch für den in v0.5.0 gemessenen HNSW-Build-Flaschenhals.

- HNSW-Traversal von String-IDs auf Integer-Slots umgestellt
- Vektoren werden im ANN-Index einmal normalisiert; Distanz-Hot-Path verwendet Dot-Products
- wiederverwendbare Visit-Generationen und typisierte Heaps reduzieren Maps/GC/Interface-Allokationen
- Edge-Similarity wird gespeichert und beim Neighbor-Pruning wiederverwendet
- Neighbor-Referenzen auf `uint32` verdichtet
- `AddBatch` korrigiert: kein manuelles O(N²)-Slice-Wachstum mehr
- Construction-Visit-Budget und Greedy-Hop-Limit begrenzen lange Worst-Case-Traversals
- HNSW-Level deterministisch aus Memory-ID abgeleitet; stabil über Restart/Rebuild
- neue kompakte binäre HNSW-Base-Snapshots; v0.5-JSON-Bases bleiben lesbar
- `cmd/bench`: `-progress-every` und konsistentes `-tier-every` für Full/Storage
- neuer Brute-Force Recall@10-Regressionstest und Binary-Snapshot-Roundtrip-Test
- gemessen: 50k/32D 6.569 s vs. 29.278 s in v0.5.0; 100k/32D 16.395 s statt >120-s-Timeout; 200k/32D 71.371 s

## v0.5.0

- `state.json` checkpoint no longer serializes an O(N) memory catalog when memory segments are enabled
- memory catalog reconstructed from latest segment records/tombstones on startup
- Hot/Cold memory-body tiering with configurable byte and age limits
- bounded LRU page cache for lazy cold-body reads
- cold metadata keeps `vector_dim`; full bodies are hydrated only when needed
- Linux mmap / ReadAt segmented storage retained and batch segment writes reduced to one fsync per batch/rotation
- HNSW Base+Delta snapshots gain background merging and manual merge endpoint
- index change shadow stores node hashes/metadata rather than a second full graph copy
- Raft-style automatic leader election: persistent terms/votes, follower/candidate/leader roles, randomized timeouts and heartbeats
- higher terms force stale leaders to step down; candidates must not have an older last-log index
- quorum Prepare/Commit remains the mutation safety barrier
- leader revalidates term/role immediately before durable commit decision
- append-only fsync replicated cluster log segments with rotation
- followers persist commit/abort decisions in their replicated log before applying prepared entries
- internal vote and heartbeat endpoints plus web/admin cluster role status
- manual/admin Hot/Cold tiering and HNSW background-merge controls
- `cmd/bench` for reproducible storage and full-HNSW synthetic benchmarks
- `AddMemoriesBatch` bulk-ingest path (max 4096 items)
- benchmarked 1,000,000-memory storage/tiering path and separate 50k full-HNSW path
- automatic migration path from v0.4; static-leader mode remains available with `auto_election=false`

### Bewusste Grenzen

Die automatische Wahl ist Raft-artig, aber kein vollständiges Raft: kein vollständiges `nextIndex/matchIndex`-Log-Matching, keine replizierten dynamischen Membership-Changes und keine generische linearizable State-Machine für sämtliche Mutationen. HNSW-Graph und dessen Vektoren bleiben im RAM; der 1-Mio-Benchmark ist ein Storage-/Tiering-Test ohne HNSW.

## v0.4.0

- segmentierter append-only Memory-Store unter `memory-segments/`
- kompakte State-Checkpoints ohne Memory-Text/Vektoren
- Linux mmap für versiegelte Segmente, plattformneutraler ReadAt-Fallback
- Segment-Rotation, Tombstones, manuelle/automatische Kompaktion
- HNSW Base-Snapshot + inkrementelle Delta-Segmente
- Hash-Shadow statt vollständiger zweiter HNSW-Snapshot-Kopie im RAM
- statischer Leader/Term/Quorum-Cluster für Memory-Writes
- fsync-durables Prepare- und Decision-Log
- persistente Pending-Entries auf Followern
- Recovery eines verpassten Commits über Leader-Decision
- Follower-Write-Forwarding an den Leader
- internes Cluster-Token und Cluster-Endpunkte
- neue Admin-Endpunkte für Storage-Status, Segment-Kompaktion und Cluster-Recovery
- Web-Dashboard für Memory-Segmente und Cluster
- `NEUROFORGE_CLUSTER_TOKEN` für Server/Docker
- Long-Context-Preisstaffel für OpenAI-Kostenkontrolle (konfigurierbar pro Modell)
- v0.3 -> v0.4 automatische Segment-Migration
- neue Tests für mmap, Segment-Checkpoint, HNSW-Deltas und 3-Node-Quorum

### Bewusste Grenze

Das Cluster-Protokoll ist ein statischer Leader mit quorum-durable Prepare/Commit und Recovery, kein vollständiges Raft/Paxos. Automatische Leader-Election und Membership-Consensus sind nicht Bestandteil von v0.4.0.

## v0.3.0

- WAL/Event-Log, Checkpoints und HNSW-Snapshot
- Truth-Key-Konflikte und Retention
- Goal-/Task-Memory und autonome Lernzyklen
- Shard-Rebalancing
