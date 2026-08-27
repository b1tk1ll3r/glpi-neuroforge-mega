# GLPI NeuroForge Mega v1.5.5

> Release: **v1.5.5** · Production-Grounding: First-Party-Quellen, Claim→Evidence-Verifikation, autoritative Action-Gates, Widerspruchserkennung und fail-closed Re-Verification.

Ein kontrolliertes Monorepo aus **GLPI AI Agent**, **GLPI AI Knowledgebase** und **NeuroForge + SQAR**. Ziel ist nicht ein untrennbarer Monolith, sondern eine gemeinsame Plattform mit klaren Zuständigkeiten, getrennten Credentials und nachvollziehbaren Failure-Modi.


## Research → Human-Review-Staging (v1.5.0)

Autonome Research-Goals können ihre quellengebundene Evidenz jetzt tatsächlich in einen **KB-Staging-Entwurf** überführen. Die Bridge ist einseitig: NeuroForge darf ausschließlich `POST /api/integrations/staging` mit dem separaten `KB_INTEGRATION_TOKEN` verwenden; `auto_reply=false` wird serverseitig erzwungen und Produktivwissen bleibt menschlich freigabepflichtig.

Der Fortschritt eines Research-Goals wird aus persistierten Research-Runs rekonstruiert (`new_evidence`, unabhängige Quellen, Corroborations) und nicht mehr nur durch eine starre `evaluation > 0.65`-Schwelle erhöht. Numerische Targets wie `100 quellengebundene Wissenseinträge` werden direkt gegen die passende Metrik gemessen. Frühere `goal-learning`-Memories werden aus der nächsten Goal-Evaluation ausgeschlossen, damit ein negativer Zyklus sich nicht selbst verstärkt.

Staging-Entwürfe sind pro Goal idempotent: Solange ein Draft aktiv im Staging liegt, wird er bei neuer Evidenz aktualisiert statt dupliziert. Nach menschlicher Promotion/Archivierung kann später wieder ein neuer Draft entstehen.

## Unified Graph Explorer (v1.4.0)

Das read-only Control Center visualisiert Runtime/Trust, Ticket-Evidence, Learning-Lineage, Research-Provenance, einen redigierten NeuroForge-Brain-Graph sowie einen reproduzierbaren Engineering-Graph aus Go-AST und Compose. Für Dateien/Symbole/Routen gibt es zusätzlich eine statische Change-Impact-/Blast-Radius-Sicht. 2D ist der operative Default; 3D ist ein optionaler, gebundener Explorer.

Der Agent stellt diese Daten ausschließlich über einen eigenen `CONTROL_READ_TOKEN` bereit. Ein optionales `codebase-memory-mcp` kann lokal für tiefere Developer-Analyse betrieben werden, ist aber keine Produktionsabhängigkeit. Siehe `docs/UNIFIED-GRAPH.md` und `docs/CODEBASE-MEMORY-MCP.md`.

## Leitprinzipien

- **Maximale Kontrolle:** GLPI-Schreibregeln, Auto-Reply-Gates, Eskalation, Idempotenz und Audit bleiben im Agenten. NeuroForge liefert semantische Evidenz, entscheidet aber nicht über Sicherheits- oder Kommunikationsregeln.
- **Maximale Effizienz:** Chunk-Vektoren können zentral in NeuroForge/HNSW/Disk-PQ liegen; das NFVJ2 Vector Journal nutzt die integrierte SQAR-Kompression. Dokument-Updates werden batchweise synchronisiert.
- **Maximale Transparenz:** `local`, `dual` und `neuroforge` erlauben einen messbaren Cutover; Fehlerverhalten ist explizit `fail-open` oder `fail-closed`; das Control Center ist read-only.
- **Human Governance:** Maschinell erzeugte Research-Vorschläge dürfen ausschließlich ins KB-Staging. Produktiv wird Wissen erst nach menschlicher Freigabe.

## Komponenten

| Komponente | Aufgabe | Schreibrechte |
|---|---|---|
| `services/agent` | GLPI-Triage, Policies, Antworten, Eskalation, Hybrid-Reranking | GLPI nach vorhandenen Policy-Gates |
| `services/knowledge` | Knowledge Authoring, Suche, Staging, Review, Promotion | Knowledge-Dateien + Staging |
| `platform/neuroforge` | semantisches Gedächtnis, HNSW/Disk-PQ, Learning, Research, SQAR-Vector-Journal | eigenes Brain-Storage |
| `services/control` | Status, aktive Migrationsparameter, Links | **keine**; read-only |
| `ollama` | gemeinsamer lokaler Inference-Endpunkt | Modellcache |

## Schnellstart

```bash
cp .env.example .env
./scripts/generate-secrets.sh
# Werte in .env übernehmen und GLPI-Zugangsdaten setzen.

./scripts/preflight.sh
docker compose pull
docker compose up -d --remove-orphans
./scripts/status.sh
```

Standardmäßig bindet der Stack nur an `127.0.0.1`:

- Control Center: `http://127.0.0.1:8070`
- GLPI Agent: `http://127.0.0.1:8080`
- Knowledgebase: `http://127.0.0.1:8081`
- NeuroForge: `http://127.0.0.1:8090/admin`
- Ollama: `http://127.0.0.1:11434`

Vor dem ersten produktiven Start bleiben in `.env.example` alle automatischen GLPI-Aktionen deaktiviert und `DRY_RUN=true`.

Die Repository-`.env.example` ist die **vollständige** Mega-Konfiguration und dokumentiert auch die weiterhin unterstützten Agent-Optionen aus älteren Installationen. Hinweise zur Migration und zu Compose-eigenen Containerwerten: [`docs/ENVIRONMENT.md`](docs/ENVIRONMENT.md) und [`docs/ENV-MIGRATION-FROM-LEGACY.md`](docs/ENV-MIGRATION-FROM-LEGACY.md).

## Kontrollierter Vektor-Cutover

`KNOWLEDGE_VECTOR_BACKEND` kennt drei Modi:

- `local`: ursprüngliches Verhalten; Chunk-Vektoren bleiben im lokalen Agent-Snapshot.
- `dual`: lokale Vektoren bleiben maßgeblich und werden zusätzlich nach NeuroForge gespiegelt. Das ist der empfohlene Beobachtungsmodus.
- `neuroforge`: NeuroForge ist für Chunk-Vektor-Persistenz und semantische Kandidatensuche maßgeblich. Titel-, Text-, Metadaten- und GLPI-Policy-Signale bleiben lokal; der Agent führt weiterhin sein deterministisches Hybrid-Reranking aus.

Mit `NEUROFORGE_FAIL_OPEN=true` kann der Agent bei Backend-Ausfall lokale/lexikalische Evidenz verwenden. Mit `false` wird ein semantischer Backend-Fehler sichtbar blockierend behandelt.

Details: [`docs/MIGRATION-CUTOVER.md`](docs/MIGRATION-CUTOVER.md).

## Kontrolliertes Lernen: erst Outcome, dann Wissen

Im produktionsnahen Standard (`NEUROFORGE_CONTROLLED_LEARNING=true`) werden rohe Chat-Eingaben und KI-Antworten **nicht automatisch** zu vertrauenswürdigem Langzeitwissen. Der Helpdesk-Lernpfad ist explizit menschlich gegated:

```text
Ticket -> KI-Vorschlag -> Techniker bestätigt/korrigiert -> auditiertes Outcome -> NeuroForge lernt
```

Im Agent-Dashboard kann ein Antwortvorschlag als **„KI-Antwort bestätigen“** oder **„KI-Antwort korrigieren“** validiert werden. Jede Entscheidung wird lokal in `ticket-outcomes.json` mit Sync-Status gespeichert. Eine spätere Korrektur überschreibt die frühere Entscheidung nicht, sondern erzeugt eine neue Revision mit `supersedes_id`. Nur `accepted` und `corrected` dürfen den Integration-Token-geschützten NeuroForge-Endpunkt `/api/v1/integrations/outcomes` verwenden; NeuroForge weist die vertrauenswürdige Provenance serverseitig zu.

Vor der Hochstufung verifiziert der Agent außerdem, dass sich der GLPI-Ticketzustand seit dem analysierten Run nicht geändert hat. Ein veralteter Run darf nicht als Trusted Outcome gelernt werden.

Standardmäßig ist `OUTCOME_LEARNING_FAIL_OPEN=false`: Kann das bestätigte Outcome nicht nach NeuroForge synchronisiert werden, sieht der Techniker einen Fehler. Der lokale Audit-Eintrag bleibt mit `sync_status=failed` für einen kontrollierten Retry erhalten.

v1.3.0 schließt den Feedback-Loop: aktive, menschlich validierte Outcomes werden bei späteren ähnlichen Tickets als **sekundäre Erfahrungs-Evidenz** aus NeuroForge abgerufen. Sie dürfen die Antwortauswahl unterstützen oder ihr widersprechen, ersetzen aber niemals die Pflicht zu einem freigegebenen Knowledge-Artikel. Korrekturen superseden den alten NeuroForge-Memory atomar; die alte Revision bleibt auditierbar, ist aber nicht mehr retrieval-aktiv.

```text
Ticket -> offizielle KB-Kandidaten
       -> aktive validierte Erfahrungen
       -> LLM-Auswahl unter Policy-Gates
       -> Techniker bestätigt/korrigiert
       -> NeuroForge Outcome Memory
       -> spätere Tickets profitieren davon
```

Die Wirkung kann read-only über `POST /api/quality/replay` gemessen werden. Der Replay-Runner berichtet u. a. Knowledge Recall@K/MRR, Outcome Recall@K/MRR und Fälle, in denen validierte Erfahrung einen Knowledge-Miss sichtbar macht. Beispiel: [`docs/QUALITY-REPLAY.md`](docs/QUALITY-REPLAY.md).

Details: [`docs/CONTROLLED-AUTONOMY.md`](docs/CONTROLLED-AUTONOMY.md).

## Optionales SearXNG / kontrollierte Autonomie

SearXNG ist ein echtes, aber **optionales** Compose-Profil. Der normale Stack startet es nicht. Research und zeitgesteuerte Autonomie besitzen getrennte Schalter:

```bash
# .env: echten SEARXNG_SECRET setzen
./scripts/research-up.sh
```

`research-up.sh` startet SearXNG sowie NeuroForge mit Research/SearXNG aktiviert. `NEUROFORGE_AUTONOMY_ENABLED` bleibt davon unberührt und ist standardmäßig `false`. Damit sind drei Betriebsstufen möglich:

1. Research aus – keine Webrecherche.
2. Research an, Autonomy aus – Recherche kann explizit/manuell angestoßen werden.
3. Research an, Autonomy an – fällige Research-Goals dürfen zyklisch selbst recherchieren.

Web-Evidence erhält bewusst niedrigere Source-Trust-Werte als menschlich bestätigte GLPI-Outcomes. Unabhängige Quellen können bestehende Evidence über die vorhandene Corroboration-Logik stärken; produktive KB-Promotion bleibt trotzdem menschlich kontrolliert.

## Research → Staging

Die Knowledgebase stellt einen getrennt authentifizierten Eingang bereit:

```text
POST /api/integrations/staging
Authorization: Bearer <KB_INTEGRATION_TOKEN>
```

Dieser Endpunkt kann **nur Staging-Entwürfe** erzeugen. Er kann keine produktiven Artikel schreiben und erzwingt `auto_reply=false`. Beispiel:

```bash
export KB_INTEGRATION_TOKEN='...'
./scripts/propose-draft.sh proposal.json
```

Die Promotion bleibt ausschließlich beim normalen KB-Review-Workflow.

### Production-Grounding (v1.5.5)

Ein erfolgreiches Research-Gate allein reicht nicht mehr für einen Staging-Artikel. Vor dem Write werden Evidence und Synthese zusätzlich fail-closed geprüft:

- First-Party-/Herstellerquellen werden gegenüber Blogs/Foren bevorzugt; produktiv ist mindestens eine autoritative Quelle erforderlich.
- Microsoft Q&A gilt bewusst nur als Vendor-Community, nicht als Primärdokumentation. Weitere First-Party-Domains können operatorseitig ergänzt werden.
- Fehlercodes, CVEs, KB-Nummern, Versionsnummern und Command-Switches, die der Draft neu einführt, müssen im Evidence-Bundle vorkommen.
- Eine separate Critic-/Verification-Stufe prüft jede materielle Draft-Aussage gegen konkrete `E*`-Evidence-IDs.
- Handlungsanweisungen benötigen bei aktiviertem Production-Gate mindestens einen autoritativen Beleg.
- Unsupported/contradicted Claims blockieren den Write. Optional ist genau ein evidence-only Grounding-Repair erlaubt; anschließend wird der komplette Draft erneut verifiziert.
- Source-Authority, Claim-Coverage, Evidence-IDs, Widersprüche und Repair-Status werden im Staging-JSON persistiert.

Die Zielgröße `research_corroborations` zählt v1.5.5 unabhängige Source-Origins statt mehrere Seiten derselben Domain künstlich als unabhängige Bestätigungen zu behandeln.

## Obsidian / llm-wiki Export

Die Wissensbasis kann in zwei Sichten als Obsidian-kompatibler Vault exportiert werden:

- Knowledgebase: `GET /api/export/obsidian` – kanonische produktive JSON-Wissensbasis.
- Agent: `GET /api/knowledge/export/obsidian` – Live-Sicht inklusive synchronisierter GLPI-KB-Artikel und, sofern die GLPI-OpenAPI sie lesbar bereitstellt, `KnowbaseItem_Item`-Verknüpfungen.

Der Vault enthält YAML-Frontmatter, `[[Wikilinks]]`, `Wiki/Schema.md`, `Wiki/index.md`, `graph.json` und ein Manifest. Beide UIs besitzen einen **„⇩ Obsidian Export“**-Button. CLI-Helfer:

```bash
./scripts/export-obsidian.sh kb ./knowledge-vault.zip
./scripts/export-obsidian.sh agent ./live-vault.zip
```

Details: [`docs/OBSIDIAN-EXPORT.md`](docs/OBSIDIAN-EXPORT.md).

## Wichtige Sicherheitsgrenzen

1. Der Agent erhält nur den NeuroForge **App Key**, niemals den Admin-Token.
2. NeuroForge erhält keine GLPI-Credentials.
3. Das Control Center besitzt keine Admin-/Editor-Credentials.
4. KB-Health ist ohne Editor-Credentials probe-fähig; alle Editor-Funktionen bleiben Basic-Auth-geschützt.
5. Research-Vorschläge haben einen separaten `KB_INTEGRATION_TOKEN` und landen ausschließlich im Staging.
6. `runs.jsonl`, State/Idempotenz, Policy-Gates und GLPI-Aktionen werden nicht in lernendes Memory verschoben.

Siehe [`docs/CONTROL-MATRIX.md`](docs/CONTROL-MATRIX.md), [`docs/CONTROL-CENTER.md`](docs/CONTROL-CENTER.md) und [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Validierung

```bash
./scripts/validate.sh
cd services/agent && go test -race ./internal/knowledge
cd ../../platform/neuroforge && go test -race ./internal/httpapi ./internal/store
```

Die importierten GLPI-Projekte wurden im Mega-Repo auf Go 1.23 normalisiert. Die komplette Testbasis läuft damit in der bereitgestellten Umgebung. Die ursprünglichen Quellarchive bleiben davon unberührt.

Für Qualitätsmessungen gegen historische Fälle:

```bash
python3 scripts/quality-replay.py docs/QUALITY-REPLAY-example.json --url http://127.0.0.1:8080
```

## Bewusst begrenzte Autonomie

Bei aktivierter Research-Autonomie darf NeuroForge selbstständig **human-review Staging-Drafts** erzeugen und bei neuer Evidence idempotent aktualisieren. Es besitzt jedoch keinen produktiven Knowledge-Promotion-Pfad: `auto_reply=false` wird am Integration-Ingress erzwungen und die Übernahme in die kanonische Knowledgebase bleibt eine menschliche Review-/Promotion-Aktion. v1.5.5 verschärft zusätzlich die Source-Authority- und Claim-Grounding-Gates vor jedem autonomen Draft-Write.

### Goal-Learning separat freigeben

Controlled Learning deaktiviert Goal-Cycle-Lernen standardmäßig. Für bewusst freigegebene manuelle oder autonome Goal-Cycles:

```env
NEUROFORGE_CONTROLLED_LEARNING=true
NEUROFORGE_GOAL_LEARNING_ENABLED=true
```

Damit bleibt rohes Chat-/Assistant-Lernen weiterhin deaktiviert.
