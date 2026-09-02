# Architektur

## Zielbild

```text
                           ┌─────────────────────┐
                           │       GLPI          │
                           └─────────┬───────────┘
                                     │
                                     ▼
                           ┌─────────────────────┐
                           │   GLPI AI Agent     │
                           │ policies / actions  │
                           └──────┬───────┬──────┘
                                  │       │ events
                   semantic query │       └──────────────┐
                                  ▼                      ▼
                     ┌─────────────────────┐      ┌──────────────┐
                     │ NeuroForge Brain    │      │ Control      │
                     │ HNSW / Disk-PQ      │      │ read-only    │
                     │ NFVJ2 + SQAR        │      └──────────────┘
                     │ memory / research   │
                     │ validated outcomes  │
                     └─────────┬───────────┘
                               │ draft proposal only
                               ▼
                     ┌─────────────────────┐
                     │ Knowledge Staging   │
                     │ human review        │
                     └─────────┬───────────┘
                               │ promote
                               ▼
                     ┌─────────────────────┐
                     │ Production KB       │
                     └─────────┬───────────┘
                               │ shared files / incremental scan
                               └──────────────► Agent
```

## Verantwortungsgrenzen

### GLPI AI Agent

Bleibt die autoritative Schicht für:

- Kategorien- und Prioritätslogik
- Eskalation
- Auto-Reply-Gates
- GLPI-Schreiboperationen
- Dry-Run
- Idempotenz und Ticket-State
- Hybrid-Scoring nach semantischer Kandidatensuche
- Quellen-Allowlisten

NeuroForge darf diese Regeln weder verändern noch umgehen.

### NeuroForge

Ist die autoritative Schicht für zentral externalisierte Chunk-Vektoren:

- namespace-isolierte Knowledge-Chunks
- HNSW-/Disk-PQ-Kandidatensuche
- Vector Journal NFVJ2
- SQAR-Kompression des Vector Journals
- Brain-/Knowledge-Events
- eigenes Learning/Research

Die neue Integrations-API ist mit dem App Key geschützt und enthält bewusst keine Admin-Funktionen.

### Knowledgebase

Bleibt die Governance-Schicht:

- kanonische JSON-Artikel
- Editor
- Suche
- Staging
- Backup
- Review
- Promotion

Maschinelle Integrationen können nur Staging-Entwürfe ablegen.

## Datenfluss für Retrieval

```text
Tickettext
  -> Embedding-Profil des Agenten
  -> NeuroForge namespace search
  -> Top-N semantische Kandidaten
  -> Agent ordnet Treffer Dokumenten zu
  -> exakte lokale Titel-/Keyword-/Kategorie-/Lexikal-Signale
  -> deterministischer Hybrid-Score
  -> bestehende GLPI Policy-Gates
  -> ggf. Aktion
```

Damit bleibt ANN ein Kandidatengenerator, nicht die finale Policy-Entscheidung.

## Persistenz

### Agent

- lokale Knowledge-Metadaten und Chunks
- Titelvektoren
- im `local`/`dual`-Modus auch Chunk-Vektoren
- Audit-/Run-/State-Dateien

### NeuroForge

- Memory/WAL/Segments
- HNSW bzw. Disk-PQ
- NFVJ2 Vector Journal
- SQAR nur auf geeigneten Vektorblöcken

### Knowledgebase

- `knowledge/` produktiv
- `staging/` ungeprüft
- `backups/` Recovery

## Konsistenzmodell

Die JSON-Knowledgebase bleibt fachlich kanonisch. NeuroForge ist ein abgeleiteter semantischer Index. Dokument-IDs und Chunk-Indizes erzeugen deterministische NeuroForge-Memory-IDs. Änderungen ersetzen Chunks batchweise; entfernte Chunks werden entfernt. Dadurch kann ein kompletter Neuaufbau aus der Knowledgebase erfolgen.

## Eventing

Die vorhandenen `brainactivity`-Clients zeigen jetzt auf:

```text
POST /api/v1/integrations/events
```

Diese Events sind Telemetrie/Audit, keine Policy-Eingaben. Beispiele sind `knowledge.search` sowie Synchronisationsereignisse.


## Kontrolliertes Lernmodell (v1.2.0)

### Human Outcome Gate

```text
Ticket -> AI proposal -> technician accept/correct
                         |
                         v
                  immutable local audit
                         | App Key
                         v
             /api/v1/integrations/outcomes
                         |
                         v
          trusted semantic outcome memory
```

Der Agent bestimmt nicht selbst die vertrauenswürdige Provenance. NeuroForge akzeptiert über diesen Pfad ausschließlich `accepted` und `corrected` und setzt `glpi.outcome.*` serverseitig. Eine spätere Korrektur wird als neue Outcome-Version mit `supersedes_id` geführt.

### Optionaler Research-Layer

```text
             [compose profile: research]
                      SearXNG
                         |
                         v
Goal/manual research -> Search -> Fetch -> Evidence
                                      |
                                      v
                         provenance + dedup +
                         independent corroboration
                                      |
                                      v
                              NeuroForge Memory
                                      |
                                      v
                              KB staging only
```

Research-Infrastruktur und zeitgesteuerte Autonomie sind getrennt. `NEUROFORGE_AUTONOMY_ENABLED=false` verhindert selbstlaufende Goal-Cycles auch dann, wenn SearXNG und manuelles Research aktiv sind.

## v1.3: Closed Outcome Feedback Loop

Menschlich validierte Helpdesk-Erfahrung besitzt einen eigenen, schmalen Retrieval-Pfad:

```text
Agent ticket query
   |--------------------------|
   v                          v
Knowledge namespace      Validated outcomes
HNSW/PQ + hybrid         active accepted/corrected only
   |                          |
   +------------+-------------+
                v
          Reply selection
                |
      Knowledge ID allow-list
                |
          Policy gates / GLPI
```

Die beiden Evidenzklassen werden absichtlich nicht vermischt. Outcome-Memories liegen im NeuroForge-Brain und sind Trust-/Revision-basiert; Knowledge bleibt die veröffentlichte Autorität. Bei Korrekturen bleiben alte Memories auditierbar, wechseln aber auf `superseded` und sind nicht mehr search-active.

Für source-begrenzte Exact-Fallbacks hält der Store einen rebuildbaren In-Memory-Index `Provenance.Source -> Memory IDs`. Damit wächst der Fallback mit der betreffenden Integration/Source statt mit dem gesamten Memory-Katalog. HNSW/Disk-PQ bleiben globale Kandidatenindizes.

Die Qualitätsmessung ist vom Schreibpfad getrennt: `/api/quality/replay` ist read-only und evaluiert live die aktuelle Knowledge-/Outcome-Retrieval-Konfiguration gegen einen bereitgestellten historischen Fallkorpus.

## v1.6.0 Master/Subagent consistency model

NeuroForge owns the authoritative durable state. Subagents are stateless executors: they register capabilities, claim only compatible jobs and return lease-fenced results. Jobs and second-phase master applies are persisted through the normal WAL/checkpoint path, so restarts do not lose queued/retrying/apply-wait work. The graph backfill uses ANN candidate selection plus bounded exact relink jobs; pairwise synapses form an n:m adjacency graph and retrieval can traverse several bounded hops. See `MASTER-SUBAGENT-ORCHESTRATOR.md`.
