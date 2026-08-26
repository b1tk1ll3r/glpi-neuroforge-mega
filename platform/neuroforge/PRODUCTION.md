# NeuroForge v0.8.2 – Production Guide

## 1. Sicherheitsgrenze

NeuroForge terminiert TLS nicht selbst. Für Netzbetrieb:

1. Server nur an Loopback oder privates Service-Netz binden.
2. TLS-Reverse-Proxy davor setzen.
3. `/internal/v1/cluster/*` ausschließlich zwischen vertrauenswürdigen Nodes erreichbar machen.
4. Admin/API/Worker/Metrics/Cluster jeweils mit eigenen zufälligen Tokens betreiben.
5. `data/` nur für den NeuroForge-Service-Account lesbar machen.

Empfohlene Environment-Secrets:

```bash
NEUROFORGE_ADMIN_TOKEN=...
NEUROFORGE_APP_API_KEY=...
NEUROFORGE_WORKER_TOKEN=...
NEUROFORGE_METRICS_TOKEN=...
NEUROFORGE_CLUSTER_TOKEN=...
OPENAI_API_KEY=...  # nur falls OpenAI genutzt wird
```

Die Secret-Datei wird mit Modus `0600` geschrieben. Das Admin-UI maskiert Secrets standardmäßig. `security.allow_secret_reveal` sollte im Produktionsbetrieb `false` bleiben.

## 2. Health/Readiness

- `/livez`: Prozess läuft.
- `/readyz`: Konfiguration ist valide, Chat-/Embedding-Routen sind konfiguriert, App-Key ist vorhanden falls verlangt und ein Cluster-Leader ist vorhanden falls Cluster aktiv ist.
- Provider-Netzwerkhealth wird absichtlich nicht bei jeder Readiness-Probe geprüft.

## 3. Ressourcen/Limits

Über `http` in der Config steuerbar:

- `read_header_timeout_seconds`
- `read_timeout_seconds`
- `write_timeout_seconds`
- `idle_timeout_seconds`
- `shutdown_timeout_seconds`
- `max_header_bytes`
- `max_body_bytes`
- `max_concurrent_requests`

Für lange lokale LLM-Aufrufe kann `write_timeout_seconds: 0` verwendet werden; damit setzt NeuroForge keine Response-Write-Deadline. Reverse Proxy/Ingress können trotzdem eigene Timeouts besitzen und müssen entsprechend konfiguriert werden. Ollama-Node `request_timeout_seconds: 0` bedeutet ebenfalls keine zusätzliche Inferenz-Deadline; Netzwerk-Dial und Healthchecks bleiben begrenzt.

## 4. Wissensqualität

Vor Autonomie zuerst unter **Wissen & Lernen** prüfen:

- Welche Quellen erzeugen Memories?
- Wie hoch sind Confidence und Reward?
- Werden Duplikate unterdrückt?
- Entstehen Konflikte?
- Welche Memories dominieren Recall und warum?
- Werden semantische Konsolidierungen aus genügend Episoden gebildet?

Empfohlene Inbetriebnahme:

1. Actor + Embedding konfigurieren.
2. Auto-Learn an, Autonomie aus.
3. Learning Policy konservativ einstellen.
4. Einige hundert reale Memories beobachten.
5. Erst dann LLM-Critic aktivieren.
6. Konsolidierung aktivieren und Ergebnisse prüfen.
7. Goal-Autonomie zuletzt aktivieren.

## 5. Backup

Ein normaler rekursiver Copy-Vorgang während paralleler Writes ist **kein garantiert atomarer Backup-Snapshot**.

Sicherste Varianten:

- Service kurz stoppen und `data/` vollständig sichern; oder
- `POST /admin/api/checkpoint`, danach einen atomaren Filesystem-/Volume-Snapshot erzeugen.

Mindestens sichern:

```text
state.json
secrets.json
vector-journal.nfv
wal/
memory-segments/
hnsw-index/
sources/          # falls ingestion.store_original=true
cluster/          # falls Cluster genutzt
```

`disk-ann/` ist abgeleitet und prinzipiell rebuildbar; Mitsichern reduziert Recovery-Zeit.

Restore immer zuerst in einer isolierten Instanz testen. Danach `/readyz`, Knowledge Explorer und mindestens eine bekannte semantische Suche prüfen.

## 6. Monitoring

Prometheus: `/metrics` mit `NEUROFORGE_METRICS_TOKEN`.

Alerts in `deploy/prometheus-alerts.yml` decken u. a. Prozessausfall, 5xx-Anstieg, Cluster-Commit-Lag, Cache-Thrashing und Budgetnähe ab. Eigene Schwellen an Hardware/Traffic anpassen.

## 7. Modellwechsel

Chat-/Critic-/Consolidator-Modelle können unabhängig gewechselt werden. Beim **Embedding-Modell** anders vorgehen: erst neuen Index/Re-Embedding-Prozess planen, da alte und neue Embeddings nicht als identischer Vektorraum angenommen werden dürfen.

## 8. Upgrade

Vor Upgrade Backup/Snapshot erstellen. v0.8 ergänzt Source-Metadaten, per-Goal Scheduling und Research rückwärtskompatibel. Legacy-Memories erhalten keine erfundene Provenance; Research bleibt nach Upgrade bewusst deaktiviert, bis es konfiguriert wird.

## 9. Bekannte Grenzen

- Knowledge-Graph im Browser ist absichtlich ein bounded view, kein Renderer für Millionen Knoten.
- Knowledge-Summary wird nur beim Öffnen des Explorers erzeugt und kann bei sehr großen Stores einen Metadaten-Scan auslösen.
- Cluster ist Raft-artig, nicht vollständiges Raft.
- TLS und per-IP Rate-Limiting gehören vor NeuroForge in den Reverse Proxy / Ingress.


## 10. Dokument-Ingestion

- `ingestion.max_document_bytes` und HTTP-Body-Limit gemeinsam dimensionieren.
- PDF benötigt `pdftotext`; das offizielle NeuroForge-v0.8-Containerimage installiert `poppler-utils`.
- `data/sources/` gehört zum Backup, wenn `ingestion.store_original=true`.
- Dokumente/Webseiten sind **untrusted evidence**. Das Modell darf Anweisungen in Quellen nicht als System-/Tool-Anweisungen behandeln.
- Bei einem Wechsel des Embedding-Modells Quellen kontrolliert re-embedden/reindexieren.

## 11. SearXNG / Research

- Eigene/private SearXNG-Instanz bevorzugen.
- In SearXNG JSON als Ausgabeformat aktivieren; sonst liefert `format=json` einen Fehler.
- SearXNG selbst kann im privaten Service-Netz liegen. Web-Zielabrufe blockieren dagegen standardmäßig Loopback/private/link-local Adressen. `allow_private_targets` nur in isolierten Netzen bewusst aktivieren.
- Web-Fetch begrenzt Timeouts, Bytes, Redirects und Zeichenanzahl.
- Suchresultate werden nicht automatisch als Wahrheit behandelt: sie erhalten Source-Provenance/Trust und durchlaufen Deduplication/Recall/Consolidation.
- Für produktive autonome Goals zunächst geringe `max_queries_per_cycle`, `max_results_per_query` und `max_pages_per_cycle` verwenden.

## 12. Goal Scheduler

Autonomie läuft pro Ziel über `next_cycle_at`. `run_on_goal_create=true` kann neue Ziele sofort fällig machen. Fehler erhöhen `consecutive_errors` und verschieben den nächsten Lauf per Backoff. Das verhindert sowohl die alte globale Wartezeit als auch enge Fehlerschleifen.


## Research-Dokumente

Für PDF-Research muss `pdftotext` (Poppler) auf dem NeuroForge-Host installiert sein. Remote-Dokumente unterliegen denselben SSRF-, Redirect- und Größenlimits wie Web-Research; Dokumente werden maximal bis `ingestion.max_document_bytes` geladen. Prüfe Lizenz-/Nutzungsbedingungen der recherchierten Quellen, bevor Originaldateien dauerhaft gespeichert werden.
## 11. Live-Research-Audit

Goal-Research erzeugt einen bounded `ResearchRun` mit maximal 600 Event-Zeilen. Die Admin-Oberfläche fragt nur Sequenz-Deltas ab (`after=<seq>`); dadurch ist die Live-Ansicht unabhängig von der Gesamtzahl der Memories.

Research-Trace-Events sind **Observability/Audit**, nicht autoritative Knowledge-State-Operationen. Sie werden während des laufenden Runs ohne separaten WAL-fsync im Store gehalten und beim Run-Abschluss als bounded Run persistiert. Source-, Memory-, Synapse- und Knowledge-Event-Durability bleibt unverändert. Bei einem Prozessabbruch kann daher der jüngste Live-Event-Tail fehlen; der Run wird nach Neustart als `interrupted` markiert. Bereits ingestierte Sources/Memories werden über deren normale WAL-/Segmentpfade wiederhergestellt.

Die `preview`-Felder im Research-Trace sind absichtlich gekürzt und enthalten keine vollständigen Dokumente. Für vollständige Inhalte den Source-/Memory-Inspector verwenden.

