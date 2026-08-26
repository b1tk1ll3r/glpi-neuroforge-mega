# Umsetzung: eigenständige KI-Läufe, Priorisierung und Eskalation

## Gelieferter Funktionsumfang

Diese Version erweitert die bestehende Ticketverarbeitung um ein generisches, abwärtskompatibles Modell für eigenständige Analyseläufe. Kategorie, Priorität, Statuszuordnung, Antwortauswahl und zeitgesteuerte Eskalation besitzen jeweils eine eigene Analyse-ID, Prompt-Version, Eingangsdaten-Snapshot, Eingangsdaten-Hash, Laufzeit, strukturierte Entscheidung, Grundcodes, Policy-Prüfungen und ein separates Action-Audit.

Die Ticketpriorisierung läuft standardmäßig im Shadow Mode. Das Modell empfiehlt eine GLPI-Priorität und kontrollierte Grundcodes; Go entscheidet anschließend deterministisch. Automatische Herabstufungen sind gesperrt, Erhöhungen je Lauf begrenzt und Live-Schreibzugriffe zusätzlich durch `AUTO_PRIORITY` und `DRY_RUN` geschützt.

Die Eskalation besitzt einen unabhängigen Scheduler und ist nicht an `date_mod` oder die normale FIFO-/Polling-Deduplizierung gebunden. Alte offene Tickets können dadurch erneut geprüft werden. Alter, Modellentscheidung, Confidence, Stufe, Grundcodes, Aktion, menschliche Aktivität, aktueller Ticketzustand und Idempotenz werden getrennt validiert. Die Eskalation unterstützt die freigegebenen Aktionen `raise_priority`, `assign_second_level`, `assign_security_team`, `notify_service_owner`, `link_major_incident` und `request_manager_review`; jede Aktion besitzt eigene Policy-, Ziel- und Idempotenzprüfungen.

Die interne Queue ist eine priorisierte Heap-Queue. Manuelle Läufe, Webhooks, Polling und Scheduler-Läufe können unterschiedlich gewichtet werden. Dedupliziert wird je Ticket und Trigger, sodass ein normaler Ticketlauf und eine zeitgesteuerte Eskalation desselben Tickets parallel vorgemerkt werden dürfen, aber nicht doppelt je Trigger.

## Diagnose und Persistenz

`runs.jsonl` bleibt der ausführliche Audit-Trail. Zusätzlich speichert `state-index.json` den kompakten Betriebszustand für die letzte erfolgreich verarbeitete Ticketversion und bereits ausgeführte Eskalationsstufen. Beide Dateien werden synchronisiert beziehungsweise atomar ersetzt. Alte Auditzeilen bleiben lesbar.

Die Diagnoseoberfläche stellt Analysen dynamisch dar. Neue Analysearten benötigen dadurch keine zusätzlichen festen Felder in der UI. Die Statusübersicht zeigt Shadow-/Live-Modus, Prioritäts- und Eskalationsmetriken sowie die wirksamen Allow- und Schwellenwerte.

## Sichere Einführung

Empfohlene erste Konfiguration:

```env
DRY_RUN=true
PRIORITY_ENABLED=true
AUTO_PRIORITY=false
ESCALATION_ENABLED=false
AUTO_ESCALATION=false
```

Nach der fachlichen Auswertung der Prioritätsläufe kann die Eskalation zunächst ebenfalls ohne Aktionen aktiviert werden:

```env
ESCALATION_ENABLED=true
AUTO_ESCALATION=false
```

Erst nach Prüfung der GLPI-Felder, Filter, Rechte und Diagnoseergebnisse sollten einzelne automatische Schreibpfade aktiviert werden. Für `AUTO_ESCALATION=true` ist ein dediziertes `GLPI_AGENT_USER_ID` erforderlich.

## Bewusst nicht automatisch aktivierte Erweiterungen

Routing zu Bearbeitergruppen, Dublettenzusammenführung und SLA-Prognosen benötigen installationsspezifische Gruppenlisten, Verknüpfungsfelder beziehungsweise belastbare historische Daten. Die generische Analyse-Run-Infrastruktur und die dynamische Diagnose sind dafür vorbereitet; ohne diese Zielsystemdaten wurden keine spekulativen GLPI-Schreiboperationen eingebaut.

## Verifikation

Vor der Auslieferung wurden ausgeführt:

```text
go test ./...
go vet ./...
go test -race ./...
node --check (Dashboard-JavaScript)
go build -trimpath -ldflags="-s -w" ./cmd/agent
```

Die automatisierten Prüfungen ersetzen keinen Shadow-Mode-Test gegen die konkrete GLPI-Installation und deren generierte OpenAPI-Beschreibung.

## Prioritätskonsistenz `priority-v3`

Der Prioritätslauf erhält konservativ extrahierte, im Ticket ausdrücklich vorhandene Belege. Ollama bleibt die entscheidende Analyseinstanz; Go validiert jedoch, dass Scope, Reason Codes und Begründung den belegten Tatsachen nicht widersprechen. Die Belege und die zusätzlichen Impact-/Urgency-/Scope-Felder werden im separaten `AnalysisRun` gespeichert.

Zusätzlich erzeugt die Kategorieanalyse einen nicht blockierenden Diagnosehinweis, wenn eine externe Knowledge-Kategorie auf eine GLPI-Kategorie mit deutlich anderem Namen gemappt ist.


## Ollama-Node-Pool

Der Ollama-Client unterstützt mehrere unabhängige Server mit Healthchecks, Least-In-Flight-, Round-Robin-, Weighted- und Fastest-Recent-Routing, per-Node-Parallelitätsgrenzen, Failover und optionaler Modelldigest-Gleichheit. Jeder KI-Analyselauf speichert den ausgewählten Node und sämtliche HTTP-Versuche unter `provider`. Der Pool erhöht Durchsatz und Verfügbarkeit, teilt jedoch kein einzelnes Modell über mehrere Rechner.
