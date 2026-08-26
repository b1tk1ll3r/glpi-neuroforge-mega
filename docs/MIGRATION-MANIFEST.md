# Migration Manifest

## NeuroForge

Neu/erweitert:

- `internal/httpapi/integration.go` – App-Key-geschützte Knowledge-/Event-Integrationsendpunkte
- `internal/httpapi/integration_api_test.go` – Lifecycle, Auth und Namespace-Isolation
- `internal/store/store.go` – provenance-gefilterte semantische Suche
- `internal/store/batch.go` – Batch-Delete zur Vermeidung mehrfacher ANN-Rebuilds
- `cmd/server/main.go` – gemeinsamer Ollama-Endpunkt per Mega-Environment
- vorhandene NFVJ2/SQAR-Vector-Journal-Migration bleibt Bestandteil der Plattform

## GLPI AI Agent

Neu/erweitert:

- `internal/knowledge/neuroforge_backend.go` – SemanticBackend + NeuroForge HTTP-Client
- `internal/knowledge/store.go` – Hybrid-Retrieval mit `local`/`dual`/`neuroforge`
- `internal/knowledge/persistent_index.go` – inkrementelle Sync-/Externalisierungslogik
- `internal/config/config.go` – kontrollierbare Backend-/Failure-Parameter
- `cmd/agent/main.go` – Backend-Wiring
- Tests für Remote-Evidenz und fail-open/fail-closed

## GLPI AI Knowledgebase

Neu/erweitert:

- `internal/staging/staging.go` – quellenbewusste Staging-Proposals
- `cmd/server/app.go` – `POST /api/integrations/staging`
- `cmd/server/main.go` – Health und Staging-Ingress ohne Weitergabe von Editor-Credentials
- Tests für Staging-only-Governance und Auth-Grenzen

## Mega Platform

Neu:

- `docker-compose.yml`
- `go.work`
- `.env.example`
- `services/control/` – read-only Control Center
- `scripts/validate.sh`
- `scripts/status.sh`
- `scripts/generate-secrets.sh`
- `scripts/propose-draft.sh`
- gemeinsame `knowledge/`, `staging/`, `backups/`
- Architektur-, Kontroll-, Cutover-, Betriebs- und Validierungsdokumentation

## Version 1.1.0 – GLPI Relations & Obsidian Export

- GLPI-KB-Sync liest verfügbare `KnowbaseItem_Item`-Relationen über die installierte OpenAPI.
- `linked_items` werden im Agent-Knowledge-Modell erhalten.
- Knowledgebase und Agent exportieren Obsidian-kompatible ZIP-Vaults.
- Exporte enthalten YAML-Frontmatter, Wikilinks, Schema, Index, Manifest und `graph.json`.
- Neuer CLI-Helfer: `scripts/export-obsidian.sh`.
- Control-Center-Sicherheitsgrenze und delegierte Interaktionswege sind in `docs/CONTROL-CENTER.md` dokumentiert.
- Repräsentativer Export-Snapshot liegt unter `exports/knowledge-obsidian-snapshot.zip`.

## Version 1.2.0 – Controlled Autonomy

### NeuroForge

- `internal/httpapi/outcomes.go` – schmaler App-Key-Pfad für menschlich validierte Ticket-Outcomes
- `internal/brain/brain.go` – interne, nicht vom JSON-Client spoofbare Trusted-Provenance-Felder
- `cmd/server/main.go` – Controlled-Learning- und Research-Bootstrap per Environment
- `deploy/learning-policy.example.json` – konservative Source-Trust-/Learning-Policy

### GLPI AI Agent

- `internal/learning/outcomes.go` – persistentes Outcome-Audit, Sync-Status, Revisionen und NeuroForge-Sink
- `internal/agent/agent.go` – Stale-Run-Prüfung und Outcome-gated Learning
- `internal/web/server.go` / Dashboard – Bestätigen/Korrigieren und Audit-Sicht
- neue Outcome-Learning-Konfiguration mit explizitem fail-open/fail-closed

### Mega Platform

- `searxng` als optionaler Compose-Profilservice `research`
- `deploy/searxng/settings.yml`
- `scripts/research-up.sh`
- separate Research-, SearXNG- und Autonomy-Schalter
- Control Center zeigt diese Betriebsmodi read-only
- `docs/CONTROLLED-AUTONOMY.md`
- `docs/MIGRATION-v1.1.0-to-v1.2.0.md`
- `RELEASE-NOTES-v1.2.0.md`
- `patches/v1.1.0-to-v1.2.0.diff`

## Version 1.3.0 – Closed Learning Loop

### NeuroForge

- `internal/store/source_index.go` – rebuildbarer Provenance-Source-Index und atomare Memory-Supersession
- `internal/store/store.go` – source-begrenzter Exact-Fallback statt Full-Catalog-Scan
- `internal/brain/brain.go` – Multi-Source-Outcome-Suche
- `internal/httpapi/outcomes.go` – aktive Outcome-Suche + Remote-Supersession
- `internal/httpapi/metrics.go` – NFVJ2/SQAR Savings-/Block-Metriken
- Tests für aktive Revision, supersedete Revision und Source-Index-Rebuild nach Neustart

### GLPI AI Agent

- `internal/learning/outcomes.go` – OutcomeRetriever über den schmalen NeuroForge-App-Key-Pfad
- `internal/agent/agent.go` – validierte Erfahrung als sekundärer Reply-Kontext + Learning/Retrieval-KPIs
- `internal/model/model.go` – auditierbare `ValidatedOutcomeEvidence`
- `internal/ollama/client.go` – Prompt-Guard: Outcome darf nur KB stützen/widerlegen, nie selbst autorisieren
- `internal/web/server.go` – read-only `/api/quality/replay` und Statusmetriken
- Dashboard zeigt verwendete Erfahrungen, Similarity, Dauer und Fehler

### Mega Platform

- `scripts/quality-replay.py`
- `docs/QUALITY-REPLAY.md`
- `docs/QUALITY-REPLAY-example.json`
- `docs/MIGRATION-v1.2.0-to-v1.3.0.md`
- `RELEASE-NOTES-v1.3.0.md`
- Control Center zeigt Outcome Retrieval und Replay-Verfügbarkeit read-only
- Upgrade-Patch: `patches/v1.2.0-to-v1.3.0.diff`
