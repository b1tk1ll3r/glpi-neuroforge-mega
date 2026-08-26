# Migration einer bisherigen Agent-.env auf den Mega-Stack

## Ergebnis der Prüfung

Die 110 Variablennamen der bisherigen Agent-Konfiguration werden im aktuellen v1.4.x-Agenten
weiterhin verwendet. Das Problem lag nicht in entfernten Agent-Funktionen, sondern in der zu kurzen
Top-Level-`.env.example` des Mega-Projekts: 86 dieser 110 bisherigen Variablen waren dort nicht
explizit dokumentiert.

Die Repository-`.env.example` ist deshalb jetzt die vollständige kanonische Vorlage.

## Werte, die Compose intern besitzt

Diese Werte sollten im Mega-Stack nicht zur Host-Konfiguration benutzt werden:

| Variable | Mega-Containerwert | Externe Einstellung |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | `AGENT_HOST_PORT` |
| `DATA_DIR` | `/app/data` | Docker Volume `agent-data` |
| `KNOWLEDGE_DIR` | `/app/knowledge` | `KB_DATA_PATH` |
| `OLLAMA_URL` | `http://ollama:11434` | Compose-Service `ollama` |
| `NEUROFORGE_URL` | `http://neuroforge:8080` | Compose-Service `neuroforge` |
| `NEUROFORGE_API_KEY` | aus `NEUROFORGE_APP_API_KEY` | `NEUROFORGE_APP_API_KEY` |
| `BRAIN_ACTIVITY_URL` | NeuroForge Event API | intern verdrahtet |

Compose überschreibt `HTTP_ADDR` jetzt explizit. Dadurch kann eine alte Standalone-Konfiguration
mit `HTTP_ADDR=:7080` nicht mehr dazu führen, dass der Container auf einem anderen Port lauscht als
Compose weiterleitet.

## Einstellungen mit relevantem Verhaltensunterschied

Beim bloßen Wechsel von der alten `.env` auf die frühere kurze Mega-`.env.example` hätten sich unter
anderem diese Werte geändert:

| Bereich | bisherige Konfiguration | Verhalten bei fehlender Variable / kurzer Mega-Vorlage |
| --- | --- | --- |
| GLPI KB Connector | aktiv | standardmäßig deaktiviert |
| GLPI-KB Auto-Reply | aktiv | standardmäßig deaktiviert |
| Knowledge Web Edit | aktiv | standardmäßig deaktiviert |
| Allowed Sources | mehrere interne/GLPI/Vendor-Quellen | nur `internal-kb` |
| Auto-Reply Sources | mehrere Quellen | nur `internal-kb` |
| Category Confidence | `0.70` | Code-Default `0.90` |
| Reply Confidence | `0.70` | Code-Default `0.97` |
| Change Lookback | `72h` | Code-Default `48h` |
| Chat-Modell | bisher z. B. `qwen3:8b` | Mega-Vorlage `gemma3` |

Diese Unterschiede sind nicht automatisch falsch, müssen aber bewusst entschieden werden.

## Neue Mega-Einstellungen

Neu gegenüber der bisherigen Agent-Only-Konfiguration sind insbesondere:

- NeuroForge Admin/App/Worker/Metrics Tokens
- `KNOWLEDGE_VECTOR_BACKEND=local|dual|neuroforge`
- NeuroForge Namespace/Search/Failure Policy
- Controlled Learning
- Human-validated Outcome Learning
- Outcome Retrieval
- Research/SearXNG
- getrennte Autonomy-Aktivierung
- Knowledge Integration Token
- scoped `CONTROL_READ_TOKEN`
- Control Center / NeuroForge / Knowledge Host Ports
- optionale Codebase-Memory-UI

## Empfohlene Migration

1. Neue `.env` aus der vollständigen `.env.example` erzeugen.
2. Neue Service-Tokens mit `./scripts/generate-secrets.sh` erzeugen.
3. GLPI-Zugangsdaten neu eintragen.
4. Gewünschte alte Retrieval-/Context-/Communication-Werte bewusst übertragen.
5. Zunächst `DRY_RUN=true` und alle automatischen Schreibaktionen deaktiviert lassen.
6. GLPI KB Sync, Knowledge Retrieval, Outcome Retrieval und NeuroForge im `dual`-Modus prüfen.
7. Erst danach einzelne Schreibfunktionen kontrolliert aktivieren.

## Credential-Hygiene

Zugangsdaten, die in Chat, Tickets, CI-Logs oder Screenshots veröffentlicht wurden, gelten nicht mehr
als geheim und sollten vor Produktionsbetrieb rotiert werden. Das betrifft insbesondere GLPI
Client-Secrets/Passwörter, Web-Passwörter und Webhook-Secrets.
