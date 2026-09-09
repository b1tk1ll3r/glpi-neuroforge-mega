# Migration: Mega v1.6.1 -> Agent + Knowledge Core

Der Core-Modus verändert das Datenformat der Knowledge-JSONs nicht.

## Variante A: Knowledge-Verzeichnis weiterverwenden

Setze in `deployments/agent/.env` und `deployments/knowledge/.env` denselben `CORE_DATA_ROOT`, sodass beide auf denselben Hostpfad zeigen.

## Variante B: Agent-Index übernehmen

Der Agent speichert seinen persistenten lokalen Knowledge-Index unter `/app/data`. Der Core-Compose bind-mountet dafür `runtime/agent-data`.
Um einen bestehenden Named Volume zu übernehmen, zuerst dessen Namen ermitteln und dann offline kopieren. Niemals das laufende Volume gleichzeitig von zwei Agent-Instanzen beschreiben lassen.

## Funktionale Änderungen gegenüber Mega

Nicht verfügbar im Core-Modus:

- NeuroForge Memories/Synapses/Graph
- autonome Research Goals
- Staging-Synthese durch NeuroForge
- CPU/GPU Subagent-Orchestrator
- Control Center

Weiter verfügbar:

- GLPI Polling/OAuth2
- Agent WebUI und Diagnose
- lokale Knowledge-Indizierung und Embeddings
- Hybrid Retrieval/RAG
- Kategorien/Keywords/Source Policy
- lokale Human-in-the-loop Lernbeispiele
- optional GLPI-KB-Synchronisation
- Knowledge Editor/Search
