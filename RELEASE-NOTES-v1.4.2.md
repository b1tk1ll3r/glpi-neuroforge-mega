# GLPI NeuroForge Mega v1.4.2

Bugfix-Release für den NeuroForge-WebUI-Tab **Wissensraum**.

## Behoben

- Leere Knowledge-Graphs werden von der Store/API-Schicht jetzt mit `nodes: []` und `edges: []` serialisiert statt mit `null`.
- Das Wissensraum-WebUI normalisiert `nodes` und `edges` defensiv, sodass auch ältere oder teilweise Antworten mit `null` keinen `.length`-Fehler mehr auslösen.
- Bei einem Graph-Ladefehler wird der Canvas auf einen definierten leeren Zustand zurückgesetzt.
- Auch Event-, Source- und Goal-Listen im NeuroForge-WebUI behandeln unerwartete `null`-Listen defensiv als `[]`.
- Regressionstest stellt sicher, dass ein leerer Knowledge-Graph nicht mehr als `null` serialisierbare Slices erzeugt.

## Validierung

- `go test ./...` für NeuroForge, Agent, Knowledgebase und Control Center: OK
- `go vet ./...` für alle vier Go-Module: OK
- NeuroForge Inline-JavaScript: `node --check` OK

Keine Datenmigration und keine ENV-Änderung erforderlich. Ein Austausch/Neustart des NeuroForge-Images reicht.
