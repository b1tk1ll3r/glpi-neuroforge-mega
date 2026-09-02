# Implementierungsstand

## Implementiert

- gemeinsames Monorepo mit `go.work`
- gemeinsamer Docker-Compose-Stack
- zentraler Ollama-Endpunkt für Agent/KB/NeuroForge
- NeuroForge + NFVJ2/SQAR Vector Journal
- App-Key-geschützte NeuroForge Knowledge Integration API
- namespace-isolierte semantische Suche
- Batch-Upsert/Batch-Delete für Knowledge-Chunks
- Agent `local` / `dual` / `neuroforge` Betriebsmodi
- explizites fail-open / fail-closed
- Remote-Semantik bleibt nur Evidenz; Agent-Hybrid-/Policy-Logik bleibt autoritativ
- inkrementelle Updates und Löschungen in NeuroForge
- vorhandene Brain-Activity-Hooks auf NeuroForge Events
- read-only Control Center
- gemeinsames produktives `knowledge/` mit KB-RW / Agent-RO
- separater Bearer-geschützter Research-/Integration-Draft-Ingress in KB-Staging
- Research-Drafts können nicht produktiv schreiben und erzwingen `auto_reply=false`
- getrennte Secrets für Admin, App, Worker, Metrics und KB-Staging-Integration
- Tests für Namespace-Isolation, Lifecycle, Fail-open/fail-closed und Staging-Governance
- optionaler SearXNG-Service als Compose-Profil `research`
- Controlled-Learning-Bootstrap ohne automatisches Chat-Input/Assistant-Output-Lernen
- Human-Outcome-Learning (`accepted`/`corrected`) über separaten App-Key-Endpunkt
- lokales Outcome-Audit mit `pending|learned|failed`, Retry und unveränderlicher Revisionskette
- Stale-Run-Schutz gegen Lernen aus überholten GLPI-Ticketzuständen
- getrennte Schalter für Research/SearXNG und zeitgesteuerte Autonomie
- persistenter Master/Subagent-Orchestrator mit Resource-/Capability-Routing, Priorität, DAG-Dependencies, Retry/Backoff und Idempotenz
- Heartbeat/Lease-Fencing inklusive Expiry-Prüfung und stale-result Schutz
- zweiphasiger `apply_wait`-Commit für Worker-Ergebnisse, die autoritativen Master-State verändern
- CPU-Subagents für `vector.relink`, GPU-Subagents für `model.chat`/`model.embed`, optional remote über `docker-compose.subagent.yml`
- bounded Knowledge-Graph-Backfill für importierte Memories (ANN-Kandidaten statt O(N²))
- echte n:m-Adjamenz, Relationstypen, Multi-Hop-Retrieval und Graph-Health-Metriken
- Memory-Version + Vector-Fingerprint-Fencing gegen Delete/Recreate-Races während Relink

## Bewusst nicht automatisiert

- Kein NeuroForge-Research-Run wird ohne expliziten Workflow automatisch zum KB-Entwurf.
- Kein KB-Entwurf wird automatisch promoted.
- Keine GLPI-Automation wird durch die Vektormigration automatisch aktiviert.
- Das Control Center besitzt keine Admin-Aktionen.

Diese Grenzen sind Teil des Kontrollmodells und können später gezielt über signierte/approvable Jobs erweitert werden.
