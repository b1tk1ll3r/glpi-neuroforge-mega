# Kontrollierter Cutover

## Phase 0 – Baseline

```env
KNOWLEDGE_VECTOR_BACKEND=local
DRY_RUN=true
AUTO_CATEGORY=false
AUTO_REPLY=false
AUTO_PRIORITY=false
AUTO_ESCALATION=false
```

Ziel: unverändertes Agent-Verhalten und Baseline-Metriken sichern.

## Phase 1 – Dual Mirror

```env
KNOWLEDGE_VECTOR_BACKEND=dual
NEUROFORGE_FAIL_OPEN=true
```

Der Agent behält lokale Chunk-Vektoren und synchronisiert dieselben Dokumente zusätzlich nach NeuroForge. Suchentscheidungen bleiben lokal. Beobachten:

- Sync-Fehler im Agent-Log
- NeuroForge Memory-/Index-Wachstum
- Vector-Journal-Größe
- Retrieval-Latenz der Baseline
- keine Änderungen an GLPI-Aktionen

Rollback: `KNOWLEDGE_VECTOR_BACKEND=local` und Agent neu starten.

## Phase 2 – NeuroForge Candidate Search

```env
KNOWLEDGE_VECTOR_BACKEND=neuroforge
NEUROFORGE_FAIL_OPEN=true
```

NeuroForge liefert semantische Kandidaten. Der Agent bleibt Besitzer des finalen Hybrid-Scores und aller Policies. Nach erfolgreicher Synchronisation können lokale Chunk-Vektoren aus dem normalen Snapshot externalisiert werden; Titelvektoren und Text-/Metadaten bleiben lokal.

Rollback: auf `dual` oder `local` zurückstellen. Die kanonischen Knowledge-JSON-Dateien sind unverändert und können den semantischen Index neu aufbauen.

## Phase 3 – Optional fail-closed

Erst nach stabiler Betriebsphase:

```env
NEUROFORGE_FAIL_OPEN=false
```

Damit werden semantische Backend-Ausfälle sichtbar blockierend statt degradierend behandelt. Diese Einstellung ist sinnvoll, wenn eine Antwort ohne zentralen semantischen Index nicht zulässig sein soll.

## Phase 4 – GLPI-Automation separat freigeben

Die Vektormigration ist **keine** Freigabe für automatische GLPI-Aktionen. Jede Automation wird unabhängig aktiviert und getestet:

```env
DRY_RUN=false
AUTO_CATEGORY=true|false
AUTO_REPLY=true|false
AUTO_PRIORITY=true|false
AUTO_ESCALATION=true|false
```

Auto-Reply sollte zuletzt aktiviert werden.

## Vergleichsstrategie

Vor dem Umschalten auf `neuroforge` sollten repräsentative Tickets in `local` und `dual` mit denselben Modellen getestet werden. Zu vergleichen sind mindestens:

- Top-1/Top-k Knowledge-ID
- semantischer Teilscore
- finaler Hybrid-Score
- Schwellenwertentscheidungen
- Kategorie-/Prioritätsentscheidung
- Antwortfreigabe
- Latenz

## Recovery

- Produktive Knowledge-JSONs sind kanonisch.
- NeuroForge-Semantik kann aus diesen Daten neu aufgebaut werden.
- Staging ist getrennt und kann nicht versehentlich produktiv werden.
- NFVJ2/SQAR ist eine Storage-Optimierung; fachliche IDs und Vektoren bleiben verlustfrei rekonstruierbar.
