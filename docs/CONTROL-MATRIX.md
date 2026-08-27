# Kontroll- und Berechtigungsmatrix

| Capability | Agent | KB Editor | NeuroForge App API | NeuroForge Admin | Control Center |
|---|---:|---:|---:|---:|---:|
| GLPI lesen | ja | nein | nein | nein | nein |
| GLPI schreiben | nur Policy-gated | nein | nein | nein | nein |
| Produktive KB lesen | ja | ja | indirekt über Sync | nein | nein |
| Produktive KB schreiben | nein | ja | nein | nein | nein |
| KB-Staging schreiben | nein | ja | über getrennten KB Integration Token möglich | nein | nein |
| KB-Staging promoten | nein | ja | nein | nein | nein |
| Knowledge-Vektoren upserten | ja, Integration Token | nein | ja | ja | nein |
| Knowledge-Vektoren suchen | ja, Integration Token | nein | ja | ja | nein |
| NeuroForge Config ändern | nein | nein | nein | ja | nein |
| NeuroForge Secrets lesen/rotieren | nein | nein | nein | ja | nein |
| Systemstatus lesen | eigene Readiness | eigene Health | Stats mit Control-Read-Token | ja | aggregiert read-only |
| Obsidian-Export | Live-Sicht inkl. GLPI-Relations | kanonische KB | nein | nein | verlinkt Ziel-UI |
| Human Outcome erfassen | ja, authentifizierter Techniker | nein | empfängt nur validated outcome | sichtbar/admin | Status read-only |
| Trusted Outcome-Source setzen | nein | nein | **serverseitig fest** | ja | nein |
| SearXNG Research | nein | nein | Research Engine via SearXNG | konfigurierbar | Status read-only |
| Autonomy aktivieren | nein | nein | nein | Betreiber/Admin bzw. Env | Status read-only |

## Credentials

- `NEUROFORGE_ADMIN_TOKEN`: nur Betreiber/Admin.
- `NEUROFORGE_APP_API_KEY`: allgemeine App-API; keine Admin-Config und keine Integration-Schreibpfade.
- `NEUROFORGE_INTEGRATION_TOKEN`: ausschließlich Agent/Knowledge-Integration, Events und validierte Outcomes.
- `NEUROFORGE_CONTROL_READ_TOKEN`: ausschließlich read-only NeuroForge-Stats/Graph für das Control Center.
- `NEUROFORGE_WORKER_TOKEN`: nur NeuroForge Worker.
- `NEUROFORGE_METRICS_TOKEN`: nur Metrics-Scraper.
- `KB_INTEGRATION_TOKEN`: ausschließlich maschineller Staging-Ingress.
- `BASIC_AUTH_USER/PASSWORD`: Knowledgebase-Editor.
- `CONTROL_BASIC_AUTH_USER/PASSWORD`: Control Center; `/healthz` bleibt öffentlich.
- `WEB_USERNAME/PASSWORD`: Agent-Webzugang.
- `SEARXNG_SECRET`: nur optionaler SearXNG-Container/Betreiber.
- GLPI-Credentials: ausschließlich Agent.

## Failure-Policy

| Einstellung | NeuroForge nicht erreichbar | Verhalten |
|---|---|---|
| `local` | irrelevant | Agent bleibt vollständig lokal |
| `dual` | Fehler wird geloggt | lokale Vektoren bleiben erhalten |
| `neuroforge` + fail-open | Fehler wird geloggt | lokale/lexikalische Evidenz soweit verfügbar |
| `neuroforge` + fail-closed | Fehler wird propagiert | semantischer Schritt blockiert kontrolliert |

## Outcome-Learning Failure-Policy

| Einstellung | NeuroForge-Sync nach Technikerentscheidung | Verhalten |
|---|---|---|
| `OUTCOME_LEARNING_ENABLED=false` | nicht ausgeführt | kein Outcome-Learning |
| enabled + `FAIL_OPEN=false` | Fehler | lokaler Audit bleibt `failed`, UI meldet Fehler |
| enabled + `FAIL_OPEN=true` | Fehler | lokaler Audit bleibt `failed`, Workflow darf fortfahren |
| enabled + Sync OK | Erfolg | Audit `learned` + NeuroForge Memory-ID |

## Nicht lernende Kontrollinformationen

Folgende Informationen bleiben absichtlich außerhalb des NeuroForge-Learnings:

- GLPI OAuth/API-Secrets
- Auto-Reply-Policy
- Eskalationsregeln
- Idempotenz-/Run-State
- Schreibfreigaben
- Source-Allowlisten
- Review-/Promotion-Status

## v1.3 zusätzliche Daten- und Aktionsgrenzen

| Akteur | Outcome suchen | Outcome lernen | Outcome superseden | Quality Replay | Auto-Reply autorisieren |
|---|---:|---:|---:|---:|---:|
| GLPI Agent Integration-Token | ja, nur aktives validated Outcome API | ja, accepted/corrected | indirekt nur über neue korrigierte Revision | nein über NeuroForge; eigener read-only Agent-Endpunkt | nur über bestehende Agent-Policies + freigegebene KB |
| Agent Web-Operator | indirekt sichtbar | explizit bestätigen/korrigieren | durch Korrektur | ja, authentifiziert/read-only | nicht durch Outcome allein |
| NeuroForge Admin | technische Brain-Administration | technisch ja | technisch ja | nein | nein |
| Control Center | Status/Konfiguration sichtbar | nein | nein | Verfügbarkeit sichtbar | nein |
| Research/SearXNG | nein | Research-Evidence, nicht trusted outcome | nein | nein | nein |

`POST /api/v1/integrations/outcomes/search` akzeptiert ausschließlich den NeuroForge Integration-Token oder Admin-Token und liefert ausschließlich aktive Memories der serverseitig festgelegten Outcome-Provenance. Es ist kein generischer Memory-Search-Endpunkt und gewährt keine Admin-Funktionen.

### v1.4 graph scopes

| Actor | Capability | Credential | Write authority |
|---|---|---|---|
| Control -> Agent | runs/evidence/learning graphs | `CONTROL_READ_TOKEN` | none |
| Control -> NeuroForge | research/brain graph | `NEUROFORGE_CONTROL_READ_TOKEN` | none through graph endpoints |
| Control -> embedded Engineering Graph | structural read | none/internal | none |
| Optional Codebase Memory MCP | developer code analysis | local process / allowed root | none in platform |
