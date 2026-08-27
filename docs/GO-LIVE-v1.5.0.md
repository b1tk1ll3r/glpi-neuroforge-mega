# Go-Live Gate – v1.5.0

## Registry und Start

Auf dem Produktionshost wird **nicht gebaut**. Voraussetzung ist ein freigegebener, unveränderlicher `IMAGE_TAG` in `.env`.

```sh
./scripts/preflight.sh
docker compose pull
docker compose up -d --remove-orphans
docker compose ps
```

Alternativ führt `./scripts/go-live.sh` genau diesen Ablauf ohne `make` aus. `IMAGE_TAG=latest`, Placeholder-Secrets, wiederverwendete Trust-Boundary-Tokens, `build:` oder `env_file:` im Produktions-Compose brechen den Preflight ab.

## Pflicht-Smoke-Test auf dem echten Host

1. Ollama enthält Chat- **und** Embedding-Modell (`gemma3`/konfiguriert und `embeddinggemma`/konfiguriert); NeuroForge `/readyz` muss 200 liefern.
2. Knowledge `/api/health` und Agent `/readyz` liefern 200; Control `/healthz` bleibt ohne Login erreichbar, alle Betriebs-/Graphseiten verlangen Control-Basic-Auth.
3. Agent kann mit dem Integration-Token Knowledge-Integration/Outcome-Pfade nutzen; der Control-Read-Token kann diese Schreibpfade nicht nutzen.
4. Einen Research-Goal manuell ausführen. Fortschritt und ein Staging-Draft müssen auch dann entstehen können, wenn `NEUROFORGE_GOAL_LEARNING_ENABLED=false` ist.
5. Einen zweiten parallelen Cycle desselben Goals auslösen; er muss `409 Conflict` erhalten.
6. Staging-Draft im Knowledge-Editor prüfen und promoten. Der Produktionsartikel erscheint genau einmal und der Draft wird archiviert.
7. NeuroForge stoppen: Knowledge und Control müssen weiterlaufen; Agent verhält sich entsprechend `KNOWLEDGE_VECTOR_BACKEND`/`NEUROFORGE_FAIL_OPEN`.
8. Vor GLPI-Schreibfreigabe einen vollständigen Ticketdurchlauf in `DRY_RUN=true` prüfen. Erst danach die gewünschten Automationen einzeln aktivieren.

Docker, eine echte GLPI-Instanz, SearXNG und Ollama stehen in der Build-/Review-Umgebung nicht zur Verfügung; dieser Host-Smoke-Test ist deshalb ein bewusstes externes Release-Gate und darf nicht als lokal bestanden markiert werden.

## v1.5.5 Production-Grounding Zusatzgate

Vor Go-Live mit autonomem Research zusätzlich verifizieren:

1. `NEUROFORGE_KB_STAGING_REQUIRE_AUTHORITATIVE_SOURCE=true` und `NEUROFORGE_KB_STAGING_VERIFY_CLAIMS=true` sind im aufgelösten Compose gesetzt.
2. Ein Test-Goal mit explizitem Herstellerbezug erzeugt mindestens eine First-Party-Query (`site:`) und nutzt mindestens eine autoritative Quelle im finalen Staging-JSON.
3. `claim_verification.verdict` ist `pass`, `claim_verification.coverage` ist `1`, `unsupported`/`contradictions` sind leer.
4. `research_authoritative_sources >= 1` und `quality_gate_version=staging-v2` sind im Draft vorhanden.
5. Ein absichtlich nicht belegter Versions-/Errorcode im Synthese-Test wird fail-closed abgewiesen.
6. Ein Blog-/Forum-only Evidence-Set erzeugt bei aktiviertem Authority-Gate keinen Staging-Artikel.


## v1.5.6 Structured-Output Zusatzgate

Für einen realen Research→Staging-Smoke-Test zusätzlich verifizieren:

1. Ein Windows-/Registry-lastiges Testziel erzeugt keine `invalid ... string escape code`-Fehler.
2. Staging-Synthese und Claim-Verifikation bleiben bei nicht reparierbarem JSON fail-closed.
3. Der resultierende Draft enthält weiterhin `human_review_required=true` und `auto_reply=false`.
4. Source-Authority und Claim-Verifikation aus v1.5.5 bleiben bestanden; JSON-Robustheit darf diese Gates nicht umgehen.


## v1.5.7 Identifier-Grounding Zusatzgate

Bei Windows-/Vendor-Artikeln dürfen normale Slash-Komposita oder URL-Pfade kein `source-unverified identifiers` auslösen. Echte CLI-Switches in Code-Spans/Fences bleiben source-verifiziert. Vor Go-Live mindestens einen Goal-Lauf mit `BIOS-/UEFI`-ähnlicher Prosa und einen Lauf mit einem belegten Slash-Command prüfen.
