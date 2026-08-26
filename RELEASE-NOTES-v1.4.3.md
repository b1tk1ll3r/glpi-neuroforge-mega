# GLPI NeuroForge Mega v1.4.3

## NeuroForge Dashboard status fix

Diese Patch-Version behebt eine Inkonsistenz zwischen der erweiterten `/admin/api/status`-Antwort und dem eingebetteten NeuroForge-Dashboard.

### Behoben

- `Memories` zeigte `0`, obwohl der Knowledge-Summary bereits semantische Memories meldete.
- `Synapsen` konnte fälschlich `0` anzeigen.
- die Seitenleiste zeigte `rev undefined`.
- die Betriebsansicht konnte HNSW-, PQ-, Job- und Maintenance-Werte als `0`/leer darstellen.

### Ursache

Der Status-Endpunkt liefert die schnellen Zähler seit der erweiterten Observability-Antwort unter `stats`, während ältere Dashboard-Teile weiterhin die ursprünglichen flachen Felder erwarteten.

### Lösung

- Der Server liefert die Status-Zähler wieder zusätzlich auf Top-Level und behält parallel das strukturierte `stats`-Objekt.
- Das WebUI normalisiert beide Antwortformen defensiv. Dadurch ist sowohl ein neues UI gegen einen älteren Server als auch ein älteres UI gegen den neuen Server robuster.
- Ein Regressionstest stellt sicher, dass die flachen Dashboard-Felder nicht erneut verschwinden.

Keine Datenmigration und keine `.env`-Änderung erforderlich.
