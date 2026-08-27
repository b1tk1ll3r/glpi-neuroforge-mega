# GLPI NeuroForge Mega v1.4.5

## Research-/Goal-Logik korrigiert

v1.4.5 behebt drei zusammenhängende Fehler im autonomen Wissenspfad:

1. **Research → KB-Staging war nicht Ende-zu-Ende verdrahtet.** Der Knowledge-Service besaß zwar den sicheren `/api/integrations/staging`-Ingress, NeuroForge hatte aber keinen Publisher und rief ihn nie auf. Deshalb konnte trotz erfolgreicher Research-Läufe kein Staging-Artikel entstehen.
2. **Goal-Fortschritt maß die falsche Größe.** `goal.progress` erhöhte sich nur um 3 Prozentpunkte, wenn `evaluation > 0.65` war. Persistierte Research-Metriken wie neue Evidenz, Quellen und Corroborations wurden ignoriert.
3. **Self-Feedback konnte die Evaluation verzerren.** Frühere `goal-learning`-Memories konnten wieder in den Recall desselben Goals gelangen und negative Bewertungen erneut verstärken.
4. **`NextAction` wurde als Suchquery recycelt.** Dadurch entstanden SearXNG-Abfragen wie `NVIDIA Review the strongest negative evidence ... next cycle` statt fachlicher NVIDIA/RTX-Recherche.

## Neue Staging-Bridge

- NeuroForge kann qualifizierte Research-Ergebnisse über einen separaten Bearer-Token an den Knowledge-Service senden.
- Standard-Gate: mindestens 4 neue/gespeicherte Evidenzen und 2 unabhängige Quellen; Corroboration ist für Human-Review-Staging standardmäßig nicht zwingend (`0`), aber konfigurierbar.
- Der Knowledge-Service erzwingt weiterhin `auto_reply=false`; kein maschineller Pfad darf direkt produktiv veröffentlichen.
- `integration_key=neuroforge-goal:<goal-id>` macht die Bridge idempotent: ein aktiver Draft wird aktualisiert statt pro Scheduler-Zyklus dupliziert.
- Draft-Metadaten enthalten Goal-/Run-ID, Evidence-/Source-Zahlen, Corroborations, Evidence-IDs und Source-URIs.
- Ohne neue Evidenz/Corroboration wird ein bestehender aktiver Draft nicht unnötig neu geschrieben.
- Staging-Fehler und Draft-ID werden direkt am Goal sichtbar und als Knowledge-Events auditiert.

## Messbarer Goal-Fortschritt

- Persistierte Research-Runs werden beim Start rückwirkend eingerechnet; bestehende Goals müssen nicht bei 0 % neu beginnen.
- Kumulative Evidence-/Source-/Corroboration-Zähler bleiben monoton, auch wenn die bounded Research-Run-Historie später alte Runs verwirft.
- Numerische Targets werden semantisch interpretiert:
  - `100 ... Wissenseinträge/Evidenzen/Claims` → Evidence-Fortschritt
  - `10 Quellen` → Source-Fortschritt
  - `5 bestätigte/corroborated ...` → Corroboration-Fortschritt
- Qualitative Ziele erhalten einen gewichteten Fortschritt aus Evidenz, Source-Diversität, Corroboration und Quality-Signal.
- Das Goal-WebUI zeigt `progress_reason`, Evidence-Zahl, Source-Zahl, Bestätigungen, Staging-Draft-ID und Staging-Fehler.

## Goal-Evaluation

- `goal-learning` / `goal-cycle` wird aus der Evidenzmenge für denselben Goal-Zyklus ausgeschlossen.
- Research-Goals interpretieren frühe Zielerreichung nicht mehr pauschal als „negative evidence“.
- Die nächste Aktion ist research-spezifisch (Quellen diversifizieren, Claims corroborieren, Staging prüfen) statt pauschal „strongest negative evidence“.
- Research-Evidence erhält zusätzlich `goal:<id>`-Tags für bessere Lineage/Graph-Navigation.
- Query-Planning verwendet ausschließlich Goal-Titel/Beschreibung/Target als Suchsubjekt; Scheduler-/NextAction-Texte werden als Meta-Prozess erkannt und verworfen.

## Konfiguration

Neue Variablen:

```env
NEUROFORGE_KB_STAGING_ENABLED=true
NEUROFORGE_KB_STAGING_MIN_EVIDENCE=4
NEUROFORGE_KB_STAGING_MIN_SOURCES=2
NEUROFORGE_KB_STAGING_MIN_CORROBORATIONS=0
NEUROFORGE_KB_STAGING_MAX_EVIDENCE=12
```

Die interne URL und das Token werden vom Root-Compose sicher verdrahtet:

```text
NEUROFORGE_KB_STAGING_URL=http://knowledge:8080/api/integrations/staging
NEUROFORGE_KB_STAGING_TOKEN=${KB_INTEGRATION_TOKEN}
```

## Validierung

- alle 4 Go-Module: `go test ./...`, `go vet ./...`, `go build ./...` **OK**
- Race: NeuroForge Brain/Store/API, Knowledge Staging/Server, Control Center **OK**
- NeuroForge- und Control-Center-Inline-JavaScript: `node --check` **OK**
- Shell-Skripte: `sh -n` **OK**
- Compose- und SearXNG-YAML: Parse **OK**
- Regressionstests für research-basierten Goal-Fortschritt, Self-Feedback-Filter, Staging-Publisher und idempotentes Staging-Update **OK**

Ein Live-Docker-/GLPI-/Internet-SearXNG-Test bleibt ein Betreiber-Smoke-Test, da Docker/Produktivdienste in der Build-Umgebung nicht verfügbar sind.

Der Upgrade-Patch wurde zusätzlich auf einen unveränderten v1.4.4-Release angewendet (`git apply --check` + `git apply`) und die betroffenen NeuroForge-/Knowledge-Pakete danach erneut getestet.
