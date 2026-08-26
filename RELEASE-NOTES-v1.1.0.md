# GLPI NeuroForge Mega 1.1.0

## Enthalten

- GLPI AI Agent mit bestehender GLPI-Ticket-/Followup-/Kategorie-/Eskalationsanbindung.
- GLPI-KB-Synchronisation inklusive verfügbarer `KnowbaseItem_Item`-Relationen.
- GLPI AI Knowledgebase mit Staging/Review/Promotion und getrenntem Integration-Draft-Token.
- NeuroForge + NFVJ2/SQAR als zentraler semantischer Vector-/Memory-Layer.
- kontrollierter Vector-Cutover: `local`, `dual`, `neuroforge`.
- read-only Control Center für Health, Readiness, aktive Betriebsparameter und Navigation.
- Obsidian-/llm-wiki-artiger Export aus Knowledgebase und Agent-Live-Sicht.
- YAML-Frontmatter, Obsidian-Wikilinks, `Wiki/Schema.md`, `Wiki/index.md`, `Wiki/graph.json` und Manifest.
- CLI-Helfer `scripts/export-obsidian.sh`.
- Beispiel-Snapshot der 103 mitgelieferten kanonischen Knowledge-Dokumente.

## Sicherheitsentscheidungen

- Keine produktiven Credentials im Release.
- Ursprüngliche lokale `.env_local` wurde bewusst ausgeschlossen.
- Agent erhält keinen NeuroForge-Admin-Token.
- Control Center bleibt ohne Schreibrechte.
- Research kann nur Staging-Drafts erzeugen; `auto_reply=false` wird erzwungen.
- GLPI-Schreibentscheidungen bleiben im deterministischen Agent-/Policy-Layer.

## Validierung

Siehe `docs/VALIDATION.md`. Docker selbst ist in der Build-/Prüfumgebung nicht installiert; daher ist der echte Container-Runtime-Smoke-Test auf dem Zielhost weiterhin erforderlich.
