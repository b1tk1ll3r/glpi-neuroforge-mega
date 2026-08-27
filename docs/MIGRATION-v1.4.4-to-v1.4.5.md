# Migration v1.4.4 → v1.4.5

Es ist **keine Datenmigration** nötig. Bestehende Research-Runs und Goals werden weiterverwendet; beim NeuroForge-Start wird der Goal-Fortschritt aus den persistierten Research-Runs rückwirkend rekonstruiert.

## Empfohlene Konfiguration

```env
NEUROFORGE_CONTROLLED_LEARNING=true
NEUROFORGE_GOAL_LEARNING_ENABLED=true
NEUROFORGE_RESEARCH_ENABLED=true
NEUROFORGE_RESEARCH_GOAL_ENABLED=true
NEUROFORGE_KB_STAGING_ENABLED=true
NEUROFORGE_KB_STAGING_MIN_EVIDENCE=4
NEUROFORGE_KB_STAGING_MIN_SOURCES=2
NEUROFORGE_KB_STAGING_MIN_CORROBORATIONS=0
```

`KB_INTEGRATION_TOKEN` muss gesetzt sein. Compose reicht denselben Wert ausschließlich als `NEUROFORGE_KB_STAGING_TOKEN` an NeuroForge und als `KB_INTEGRATION_TOKEN` an den Knowledge-Service weiter.

## Verhalten nach dem Upgrade

- vorhandene Research-Goals zeigen nach Neustart einen aus ihren historischen Runs berechneten Fortschritt;
- beim nächsten qualifizierten Goal-Cycle wird ein Human-Review-Draft angelegt;
- weitere Zyklen mit neuer Evidenz aktualisieren denselben aktiven Draft;
- ohne neue Evidenz wird der Draft nicht erneut geschrieben;
- Promotion bleibt ausschließlich menschlich im Knowledge-Editor möglich.

Rollback auf v1.4.4 ist möglich. Bereits erzeugte Staging-Dateien sind normale Knowledge-Staging-JSONs und bleiben erhalten; v1.4.4 würde sie lediglich nicht mehr autonom aktualisieren.
