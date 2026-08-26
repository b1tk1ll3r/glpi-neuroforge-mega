# GLPI NeuroForge Mega v1.4.4

## Wissensraum / Legacy-Segment-ID Fix

Diese Patch-Version behebt einen Bestandsdatenfehler, der sich als **leerer Wissensraum trotz vorhandener semantischer Memories** zeigt.

### Symptom

- Übersicht/Knowledge-Summary meldet vorhandene semantische Memories.
- Research lernt weiter und die Store-Revision steigt.
- Der Tab **Wissensraum** zeigt dennoch `0 Knoten`.
- Bei betroffenen Altbeständen können zusätzlich HNSW-/Detail-/Retention-Pfade inkonsistent mit dem Memory-Katalog arbeiten.

### Ursache

NeuroForge verwendet in segmentbasierten Bestandsdaten zwei Identitätsträger:

1. den kanonischen Schlüssel bzw. `segmentRecord.ID`, und
2. die eingebettete `Memory.ID` im Segment-Payload.

Aktuelle Writer setzen beide Werte. Historische/legacy Segment-Payloads können jedoch eine gültige äußere ID besitzen, während `Memory.ID` leer ist. Die Summary zählt den Katalogeintrag korrekt, der bisherige Knowledge-Graph verwendete beim Seed-Aufbau aber `Memory.ID`. Dadurch wurde ein leerer Seed (`""`) gewählt und kein Knoten aufgelöst.

Das Fehlerbild wurde mit einem historischen Segment-Fixture auf v1.4.3 reproduziert.

### Lösung

- Der **Katalog-/Segment-Schlüssel ist kanonisch** und repariert beim Öffnen eine fehlende oder abweichende eingebettete `Memory.ID`.
- `SegmentStore.Get()` stellt die kanonische ID auch beim Hydratisieren alter Bodies wieder her.
- Knowledge-Graph, Knowledge-Listen, Provenance-Lookups, Child-/Supersession-Navigation, Retention und Hot-HNSW verwenden robuste kanonische IDs.
- Der Knowledge-Graph liefert zusätzlich:
  - `total_memories`
  - `total_synapses`
  - `truncated`
- Das WebUI zeigt dadurch beispielsweise `64 / 163 Knoten · 0 Kanten` statt den Graph-Sample mit dem Gesamtbestand zu verwechseln.
- Solange noch **keine Synapsen** existieren, zeigt der Wissensraum einen gebundenen Start-Sample von bis zu 64 isolierten Memories. Sobald Synapsen entstehen, übernimmt wieder die graphbasierte Expansion.

### Datenmigration

Keine manuelle Datenmigration ist erforderlich. Die Identitätsnormalisierung erfolgt beim Öffnen des Stores. Bestehende Segmentdateien werden nicht destruktiv umgeschrieben.

### Validierung

- Legacy-Segment mit leerer eingebetteter `Memory.ID`: vorher Graph leer, nach Fix Graph + Retrieval korrekt.
- `go test ./...`, `go vet ./...`, `go build ./...` für alle vier Module: OK.
- NeuroForge Store/HTTP API Race-Checks: OK.
- NeuroForge WebUI JavaScript Syntaxcheck: OK.

Nach dem Update genügt ein Neustart des NeuroForge-Containers.
