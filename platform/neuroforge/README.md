# NeuroForge v0.8.3

NeuroForge ist eine persistente, assoziativ lernende KI-Schicht in Go. Ollama und optional OpenAI liefern Inferenz/Embeddings; NeuroForge besitzt den dauerhaften Wissenszustand: Vektoren, HNSW/Disk-PQ-Recall, Synapsen, Rewards, Provenance, Konflikte, Konsolidierung, Goals und Learning Cycles.

**v0.8.3 übernimmt den v0.8.2-Funktionsumfang und härtet große Bulk-/Graph-Workloads gegen Speicher- und Recovery-Spitzen. v0.8.2 erweitert den Production-/Explainability-Stand um einen source-grounded Lernpfad, Dokument-/Text-Ingestion, SearXNG-Research und ein vollständig neu gestaltetes CSS/Vanilla-JS-Admin-UI mit responsive Knowledge-Graph und Level-of-Detail (LOD).** Wissen soll nicht nur gespeichert, sondern als Kette `Quelle → Evidence → Recall → Learning` nachvollziehbar sein.


## Neu in v0.8.2: Live Research pro Goal

Im Admin-Bereich **Ziele & Autonomie** kann für jedes Research-Goal die Ansicht **Live Research** geöffnet werden. Ein autonomer oder manuell gestarteter Research-Lauf wird als persistenter, bounded Run mit sequenzierten Events nachvollziehbar:

```text
Query-Planung
  ↓
SearXNG Queries
  ↓
Treffer / URLs
  ↓
Downloads / Dokumenterkennung
  ↓
Claim-Kandidaten / Evidence-Chunks
  ↓
Embedding + Dedup
  ↓
neu gelernt / corroborated / verworfen
  ↓
Run abgeschlossen
```

Die Oberfläche zeigt live Treffer, laufende/abgeschlossene Downloads, Claim-/Evidence-Kandidaten, neue Memories, Duplikate, unabhängige Bestätigungen sowie verworfene Quellen und Fehler. Claim-Kandidaten sind bewusst **keine automatisch als wahr markierten Fakten**: Sie sind kurze, transparente Auszüge aus den tatsächlich extrahierten Source-Chunks; Verifikation entsteht weiterhin über Source-Provenance, Dedup/Korroboration, Confidence und den normalen Lernpfad.

Die Live-API ist inkrementell und scannt nicht den Wissensbestand:

```text
GET /api/v1/goals/{id}/research/live?run_id=<run>&after=<seq>
GET /api/v1/goals/{id}/research/history?limit=10
```

Der Browser pollt nur Events nach der letzten Sequenznummer. Pro Run werden höchstens 600 Event-Zeilen als Audit-Tail gehalten; Run-Historie ist ebenfalls begrenzt. Live-Events verursachen **keinen WAL-fsync pro Event**: autoritative Source-/Memory-Writes behalten ihre normale Durability, während der Research-Trace beim Run-Abschluss kompakt persistiert wird. Nach einem Crash wird ein zuvor laufender Trace als `interrupted` markiert.

## Neu in v0.8.1: Goal-Steuerung & SearXNG-Dokumente

- Goals lassen sich im Admin-Dashboard **pausieren**, **fortsetzen** und **löschen**. Pausierte Goals besitzen keinen nächsten Scheduler-Termin und werden von Autonomie vollständig übersprungen. Beim Fortsetzen wird ein Auto-Goal wieder eingeplant. Das Löschen eines Goals löscht **nicht** bereits gelerntes Wissen.
- SearXNG-Dateitreffer werden beim aktivierten Abruf als Dokumente erkannt und über die normale Dokument-Ingestion verarbeitet. Unterstützt sind PDF, DOCX, TXT, Markdown, CSV/TSV, JSON und YAML; Erkennung erfolgt über SearXNG-Dateimetadaten, URL, `Content-Disposition` und den tatsächlichen HTTP-Content-Type.
- Remote-Dokumente behalten Source-URI, Dateiname, MIME-Typ und Provenance. Wenn `ingestion.store_original=true` ist, wird auch die Originaldatei im Source-Blob-Store abgelegt.

## Neu in v0.8: Quellenbasierte Wissensanreicherung

Der bevorzugte Lernpfad für externes Wissen ist jetzt:

```text
Text / Dokument / SearXNG
        ↓
Knowledge Source + Provenance
        ↓
Extraktion / Normalisierung
        ↓
überlappende Chunks
        ↓
Embedding + Duplicate-Suppression
        ↓
Evidence Memories
        ↓
Recall / Goal Research
        ↓
Evaluate / Consolidate / Learn
```

Unter **Quellen & Import** können Text sowie TXT, Markdown, HTML, JSON, CSV/TSV, DOCX und PDF hochgeladen werden. PDF-Extraktion verwendet `pdftotext` aus Poppler; das Server-Containerimage enthält `poppler-utils`. Originaldateien können zusätzlich unter `data/sources/` gespeichert werden. Jedes Evidence-Memory trägt Source-ID, URI/Titel, Chunk-Nummer, Content-Hash sowie verwendetes Embedding-Modell/-Node. Nahezu identische Evidenz aus einer **anderen** Quelle wird nicht einfach verworfen: NeuroForge verknüpft sie als unabhängige Korroboration mit dem bestehenden Evidence-Memory und erhöht dessen Confidence vorsichtig; dieselbe Source zählt nicht doppelt.

REST:

```text
POST /api/v1/ingest/text
POST /api/v1/ingest/document
GET  /api/v1/sources
GET  /api/v1/sources/{id}
POST /api/v1/research
```

### SearXNG Research

Im Admin-Reiter **Research** wird eine private SearXNG-Instanz als Suchbackend konfiguriert. NeuroForge fragt deren JSON-API ab, kann ausgewählte Trefferseiten abrufen und speichert Such-Snippets/Webseiten ausschließlich als quellengebundene Evidence. Web-/Dokumenttext wird in LLM-Prompts als **untrusted data** behandelt; darin enthaltene Anweisungen dürfen nicht als System-/Tool-Anweisungen ausgeführt werden. Private/Loopback/Link-Local-Ziele aus Suchtreffern sind standardmäßig blockiert (`allow_private_targets=false`).

Für eine SearXNG-Instanz muss JSON-Ausgabe aktiviert sein. Ein minimales Override liegt unter `deploy/searxng/settings.yml.example`. Die SearXNG-URL selbst darf intern sein, z. B. `http://searxng:8080`; nur die von Suchergebnissen ausgehenden Seitenabrufe unterliegen dem SSRF-Guard.

### Goal Research

Autonome Ziele haben jetzt einen **eigenen Zeitplan** (`auto_run`, `interval_minutes`, `next_cycle_at`) und können Research pro Ziel aktivieren. Bei einem fälligen Zyklus läuft optional:

```text
Research Queries → SearXNG → Web Evidence → Recall → Predict → Evaluate → Learn
```

Neue Ziele können mit `autonomy.run_on_goal_create=true` sofort fällig werden. `research.goal.search_every_cycle` steuert, ob bei jedem Zyklus neu gesucht wird. Fehler erzeugen Backoff statt einen aggressiven Retry-Loop.

### Responsive Knowledge Graph mit LOD

Das neue Admin-UI benötigt keine JS/CSS-Frameworks. Der Canvas-Graph wechselt abhängig vom Zoomlevel automatisch zwischen:

- **Übersicht:** aggregierte Memory-Typ-Cluster,
- **Mittel:** einzelne Knoten + reduzierte starke Kanten,
- **Detail:** mehr Kanten, Labels und selektierter Kontext.

Pan/Zoom und Knoteninspektion laufen clientseitig; der Server liefert weiterhin begrenzte Graph-Fenster statt Millionen Knoten in den Browser zu drücken.

## Schnellstart

```bash
ollama pull <DEIN_CHAT_MODELL>
ollama pull <DEIN_EMBEDDING_MODELL>

go run ./cmd/server -data ./data
```

Admin: `http://localhost:8080/admin`

Der Admin-Token wird im Production-Default **nicht ins Log geschrieben**. Setze ihn vorzugsweise selbst:

```bash
export NEUROFORGE_ADMIN_TOKEN='lange-zufällige-Zeichenfolge'
export NEUROFORGE_APP_API_KEY='lange-zufällige-Zeichenfolge'
export NEUROFORGE_WORKER_TOKEN='lange-zufällige-Zeichenfolge'
export NEUROFORGE_METRICS_TOKEN='lange-zufällige-Zeichenfolge'
```

Ohne `NEUROFORGE_ADMIN_TOKEN` wird beim ersten Start einer erzeugt und lokal mit Modus `0600` in `data/secrets.json` abgelegt.

## Wissen nachvollziehen

Im Admin-Reiter **Wissen & Lernen** findest du:

- eine Pipeline `Input → Embedding → Recall → Actor → Learn → Reward → Synapsen → Konsolidierung`
- Verteilung nach episodischem, semantischem, prozeduralem und Working Memory
- Status `active`, `conflicted`, `superseded`, `archived`
- Herkunft/Provenance jedes neuen v0.7-Memorys
- responsiver Synapsen-/Memory-Graph mit Canvas-LOD und bounded server-side graph window
- Parent-/Child-, Konsolidierungs- und Truth-Version-Beziehungen
- persistente Learning Timeline
- Explainable Recall: echte Score-Zerlegung pro Treffer

Recall wird erklärt als:

```text
score = similarity × salience_factor × type_weight × confidence_factor + graph_boost
```

Zusätzlich wird gezeigt, ob ein Kandidat aus `hnsw`, `disk-pq`, `scan` oder einer `synapse`-Expansion stammt. Der endgültige Similarity-Wert wird gegen den Originalvektor berechnet, sofern er verfügbar ist.

Legacy-Memories aus älteren Versionen bleiben lesbar. Für Provenance, die damals nicht erfasst wurde, zeigt das UI ausdrücklich `legacy/unknown`, statt Herkunft zu erfinden.

## Learning Policy

Die Learning Policy wird direkt im Knowledge Explorer konfiguriert. Sie steuert:

- globales Auto-Learn
- Chat-Eingaben speichern: ja/nein
- Chat-Antworten speichern: ja/nein
- explizites `POST /api/v1/learn` erlauben
- Imports erlauben
- Goal-Cycles dauerhaft lernen lassen
- minimale Confidence
- Duplicate-Similarity-Schwelle
- Mindestzahl bestätigender Episoden vor semantischer Konsolidierung
- minimale semantische Confidence
- automatisches Archivieren stark negativ bewerteter Assistant-Memories
- maximale Memory-Textlänge
- Quellenvertrauen für `chat.input`, `chat.response`, `api.learn`, `api.import`, `ingest.text`, `ingest.document`, `web.search`, `web.page`, `goal-cycle`, `consolidation`

Fast identische Memories werden bei aktivierter Duplicate-Schwelle nicht erneut angelegt (Truth-Key-Versionierungen bleiben davon ausgenommen). Learning-Policy-Entscheidungen erscheinen in der Timeline.

Admin API:

```text
GET /admin/api/learning-policy
PUT /admin/api/learning-policy
```

## Ollama / Modellrollen

Unter **Modelle & Routing** lassen sich mehrere Ollama-Server konfigurieren. Jeder Node besitzt getrennte Chat- und Embedding-Modellfelder. Logical Roles können zusätzlich fest gepinnt werden:

- Actor / Chat
- Embedding
- Critic (LLM Auto-Reward)
- Consolidator
- Goal-Learning

Ungepinnte Ollama-Routen verwenden gewichtetes Failover. Ein expliziter Node-Pin ist absichtlich strikt, damit eine Qualitätsrolle nicht still auf ein anderes Modell fällt.

Beispiel:

```json
{
  "routing": {
    "chat_provider": "ollama",
    "embedding_provider": "ollama",
    "chat_node_id": "brain-01",
    "embedding_node_id": "brain-01",
    "critic": {"provider":"ollama","node_id":"critic-01"},
    "consolidator": {"provider":"ollama","node_id":"brain-01"}
  },
  "ollama": [
    {
      "id": "brain-01",
      "name": "Primary Brain",
      "base_url": "http://10.0.0.11:11434",
      "chat_model": "<CHAT_MODEL>",
      "embedding_model": "<EMBED_MODEL>",
      "weight": 1,
      "enabled": true,
      "request_timeout_seconds": 0,
      "num_ctx": 8192,
      "num_predict": 0,
      "think": "off",
      "chat_keep_alive": "30m",
      "embedding_keep_alive": "5m"
    }
  ]
}
```

Direkt verwendbar über `PUT /admin/api/model-routing` mit `X-Admin-Token`.

### Long-running Ollama inference

Ab v0.7.3 gibt es keinen globalen 120-Sekunden-Client-Timeout mehr. Pro Ollama-Node gilt:

- `request_timeout_seconds: 0` = keine zusätzliche Inferenz-Deadline; Request endet nur durch Client-Abbruch/Server-Shutdown oder einen explizit gesetzten Timeout.
- `num_ctx` wird als Ollama-Runtime-Option weitergereicht; `0` lässt Ollama/Modell entscheiden.
- `num_predict` wird als Ollama-Runtime-Option weitergereicht; `0` erbt das NeuroForge-Output-Limit.
- `think` erlaubt `off`, `on`, `low`, `medium`, `high`, `max`.
- `chat_keep_alive` und `embedding_keep_alive` steuern getrennt, wie lange Ollama die jeweiligen Modelle geladen hält.

Für wirklich unbegrenzte Browser-Requests zusätzlich `http.write_timeout_seconds: 0` setzen. `0` deaktiviert nur das Response-Write-Limit; Header-/Read-/Idle-/Shutdown-Schutz bleibt separat konfigurierbar.

**Embedding-Modell nicht unkoordiniert wechseln.** Ein anderes Embedding-Modell kann einen anderen Vektorraum erzeugen; bestehendes Wissen sollte dann kontrolliert re-embedded/reindexed werden.

## Production HTTP

Neu in v0.7:

```text
GET /livez    Prozess lebt
GET /readyz   Config/Routes/Cluster sind betriebsbereit
GET /healthz  Kompatibilitätsalias zu /livez
GET /version
GET /metrics  Prometheus, Bearer Metrics-Token
```

`/readyz` führt absichtlich **keinen Netzwerk-Call zu Ollama/OpenAI** pro Probe aus. Provider-Liveness wird explizit über „Verbindungen prüfen“ getestet; so macht Kubernetes/Docker-Healthchecking die Modellserver nicht selbst zum Lastgenerator.

HTTP-Härtung:

- `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`
- maximales Request-Body- und Header-Limit
- globale Concurrent-Request-Grenze mit `503 + Retry-After`
- Security Header + CSP
- Constant-Time Tokenvergleich
- graceful SIGTERM/SIGINT shutdown + finaler Checkpoint
- Admin-Secrets standardmäßig maskiert
- Secret-Reveal standardmäßig deaktiviert

## Docker

```bash
cp .env.example .env
# starke Tokens in .env eintragen
docker compose up --build -d
```

Der Compose-Default bindet den HTTP-Port nur auf `127.0.0.1:8080`; für externen Zugriff einen TLS-Reverse-Proxy davor setzen. Der Container läuft non-root, mit read-only Root-Filesystem, `no-new-privileges` und gedroppten Capabilities.

## Prometheus

```yaml
scrape_configs:
  - job_name: neuroforge
    static_configs:
      - targets: ['neuroforge:8080']
    authorization:
      credentials: '<METRICS_TOKEN>'
```

Beispiel-Alerts: `deploy/prometheus-alerts.yml`.

Metrics vermeiden Memory-/Goal-/Session-IDs und Inhalte als Labels. Der reguläre `/metrics`-Scrape verwendet O(1)-artige Store-Zähler statt eines Vollscans über alle Memories.

## Storage

Typisches Datenverzeichnis:

```text
data/
├── state.json
├── secrets.json
├── vector-journal.nfv
├── wal/
├── memory-segments/
├── hnsw-index/
├── disk-ann/
├── sources/        # Originale, falls store_original=true
└── cluster/
```

Die segmentierten Memory-Dateien sind Source of Truth für ausgelagerte Bodies/Vektoren. Disk-PQ ist ein abgeleiteter ANN-Index und kann neu gebaut werden, darf bei einer Disaster-Recovery-Sicherung aber gern mitgesichert werden, um Rebuild-Zeit zu sparen.

`vector-journal.nfv` ist weiterhin ein rebuildbarer Beschleunigungs-Cache. Neue Journale verwenden `NFVJ2`: gleichdimensionale Vektoren werden blockweise gespeichert und ab 64 KiB automatisch mit einem SQAR-abgeleiteten 2D-Transform + DEFLATE verglichen. Nur eine tatsächlich kleinere Darstellung wird übernommen; kleine Blöcke bleiben roh. Bestehende `NFVJ1`-Dateien werden beim Öffnen atomar auf V2 migriert und bleiben bei einem fehlgeschlagenen Upgrade weiterhin lesbar.

Relevante Storage-Konfiguration:

```json
{
  "storage": {
    "vector_journal": {
      "compression": "sqar-auto",
      "block_vectors": 128,
      "min_block_bytes": 65536,
      "min_savings_pct": 0.01
    }
  }
}
```

Mit `compression: "off"` werden neue V2-Blöcke ohne Kompression geschrieben. Die Memory-Segmente selbst bleiben absichtlich unverändert, damit mmap und per-record Random Access nicht durch eine Ganzdatei-Kompression verschlechtert werden.

## API-Auswahl

Application API (`Authorization: Bearer <APP_API_KEY>`):

```text
POST   /api/v1/chat
POST   /api/v1/learn
POST   /api/v1/search
POST   /api/v1/search/vector
POST   /api/v1/memory/import
POST   /api/v1/feedback
POST   /api/v1/ingest/text
POST   /api/v1/ingest/document
GET    /api/v1/sources
GET    /api/v1/sources/{id}
POST   /api/v1/research
GET    /api/v1/stats
GET    /api/v1/goals
POST   /api/v1/goals
POST   /api/v1/goals/{id}/cycle
GET    /api/v1/learning-cycles
GET    /api/v1/conflicts
```

Explainability/Admin (`X-Admin-Token`):

```text
GET    /admin/api/knowledge/summary
GET    /admin/api/knowledge/memories
GET    /admin/api/knowledge/memory/{id}
GET    /admin/api/knowledge/graph
GET    /admin/api/knowledge/events
POST   /admin/api/knowledge/search
GET    /admin/api/learning-policy
PUT    /admin/api/learning-policy
GET    /admin/api/model-routing
PUT    /admin/api/model-routing
GET    /admin/api/research
PUT    /admin/api/research
POST   /admin/api/research/test
```

Vollständig: `openapi.yaml`.

## Cluster-Hinweis

Der Cluster besitzt persistente Terms/Votes, Heartbeats und quorum-durable Prepare/Commit-Logik. Er ist **Raft-artig**, aber nicht als vollständige Raft-Implementierung zu bezeichnen: vollständiges Log-Matching/Membership-Consensus sind weiterhin Grenzen. Cluster-Endpunkte gehören in ein privates Netz und dürfen nicht direkt ins öffentliche Internet.

## Backup / Restore

Siehe `PRODUCTION.md`. Kurzfassung: Vor einem konsistenten Dateibackup Schreibverkehr stoppen oder einen storage-seitigen atomaren Snapshot verwenden; vorher `POST /admin/api/checkpoint` auslösen. Mindestens `state.json`, `secrets.json`, `vector-journal.nfv`, `wal/`, `memory-segments/`, `hnsw-index/`, bei gespeicherten Originalen `sources/` und bei Clusterbetrieb `cluster/` sichern.

## Validierung

Release-Prüfung:

```bash
go test ./...
go vet ./...
go test -race ./internal/store
go test -race ./internal/brain
go test -race ./internal/httpapi
go test -race ./internal/vector
```

Der konkrete Release-Stand und Smoke-Tests stehen in `VALIDATION-v0.8.2.txt`.
