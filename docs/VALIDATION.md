# Validierung

Stand: 26.08.2026 — Release v1.4.4

## Umfang

- 4 Go-Module im gemeinsamen `go.work`
- 159 Go-Dateien
- 47.489 Go-Codezeilen inklusive Tests
- 281 `Test...`-Testfunktionen
- 103 produktive Knowledge-JSON-Dateien
- 8 Compose-Services inklusive optionalem SearXNG-Profil
- reproduzierbarer Engineering-Snapshot: 1.652 Knoten / 6.450 Kanten

## Vollständige Modulprüfung

```text
platform/neuroforge   go test ./...   OK
platform/neuroforge   go vet  ./...   OK
platform/neuroforge   go build ./...  OK
services/agent        go test ./...   OK
services/agent        go vet  ./...   OK
services/agent        go build ./...  OK
services/knowledge    go test ./...   OK
services/knowledge    go vet  ./...   OK
services/knowledge    go build ./...  OK
services/control      go test ./...   OK
services/control      go vet  ./...   OK
services/control      go build ./...  OK
```

Shell-Syntax (`scripts/*.sh`), Control-Center-JavaScript (`node --check`), Root-Compose und SearXNG-YAML wurden zusätzlich erfolgreich geprüft. `make engineering-graph-check` bestätigt, dass der eingebettete Engineering-Graph zum Quellstand passt.

## v1.4-spezifische Prüfungen

- Agent-Control-Endpunkte verlangen den separaten Bearer `CONTROL_READ_TOKEN`: **OK**
- Ticket-Evidence-Graph enthält Knowledge, validierte Outcomes, Policy-Checks, Reply und Human Outcome: **OK**
- Learning-Lineage erhält `supersedes`-Revisionen: **OK**
- NeuroForge Research-/Brain-Graph verlangen den App-Key: **OK**
- Brain-Graph ist gebunden/redigiert; Vektoren und voller Memory-Text werden nicht exportiert: **OK**
- Research-Graph bildet Query -> Source -> learned Memory ab: **OK**
- Engineering-Graph enthält Component/Package/File/Function/Route/Service-Knoten: **OK**
- Engineering-Endpunkt respektiert Node-Budgets: **OK**
- Change-Impact verlangt eine explizite Query, bleibt gebunden und liefert Risk-Metadaten: **OK**
- 2D/3D-Canvas-JavaScript besteht Syntaxprüfung: **OK**
- optionales Codebase Memory MCP beeinflusst Readiness nicht: konstruktiv durch `Optional`-Target / leere Default-URL abgesichert

## Race-Checks der neuen Pfade

```text
services/control      go test -race ./...               OK
services/agent        go test -race ./internal/web      OK
platform/neuroforge   go test -race ./internal/httpapi  OK
```

Ein parallel gestarteter Sammel-Race-Lauf lief in das globale Ausführungszeitlimit; die v1.4-betroffenen Pakete wurden deshalb anschließend einzeln erfolgreich geprüft. Ein Timeout wird nicht als Testerfolg gewertet.

## Weiterhin erhaltene Kernfunktionen

Die bestehende Regressionstestbasis umfasst weiterhin GLPI Polling/Webhook/Followups/Kategorien/Priorität/Eskalation, kontrolliertes Outcome-Learning und Supersession, Outcome-Retrieval, Quality Replay, Knowledge `local|dual|neuroforge`, HNSW/Disk-PQ, NFVJ2/SQAR, SearXNG Research, Obsidian-Export und Staging-Governance.

## Nicht als getestet behauptet

Docker/Podman sind in der Prüfungsumgebung nicht installiert. Deshalb wurden nicht ausgeführt:

- echter `docker compose up`
- Live-SearXNG gegen das Internet
- Live-GLPI gegen die Betreiberinstanz
- optionales Codebase Memory MCP als realer externer Prozess
- historischer Quality-Replay mit echten Betreiber-Tickets

Vor Produktivfreigabe bleiben Container-Smoke-Test, echte GLPI-/Research-Konnektivität und der historische Quality-Replay Betreiber-Gates.

## v1.4.4-spezifische Prüfungen

- Historischer Segmentdatensatz mit kanonischer `segmentRecord.ID`, aber leerer eingebetteter `Memory.ID`: **reproduziert auf v1.4.3, behoben auf v1.4.4**
- Legacy-ID wird beim Segment-Scan, beim vollständigen Segment-Read und beim Store-Migrationspfad aus dem kanonischen Katalogschlüssel wiederhergestellt: **OK**
- Derselbe Legacy-Memory erscheint anschließend wieder im Knowledge Graph und in der Vektorsuche: **OK**
- Knowledge-Graph liefert `total_memories`, `total_synapses` und `truncated` für transparente Sampling-Anzeige: **OK**
- Ohne Synapsen zeigt der Graph einen gebundenen Start-Sample von bis zu 64 Memories statt leer zu wirken: **OK**
- NeuroForge `go test ./...`, `go vet ./...`, `go build ./...`: **OK**
- NeuroForge `go test -race ./internal/store ./internal/httpapi`: **OK**
- eingebettetes NeuroForge-JavaScript `node --check`: **OK**
