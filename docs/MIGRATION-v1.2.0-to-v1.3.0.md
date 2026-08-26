# Migration v1.2.0 -> v1.3.0

## Ziel

v1.3.0 schließt den Outcome-Learning-Kreis und ergänzt Messbarkeit. Bestehende v1.2.0-Outcomes bleiben kompatibel; neue Korrekturen können ihre Vorgänger in NeuroForge tatsächlich superseden.

## Neue Konfiguration

```env
OUTCOME_RETRIEVAL_ENABLED=true
OUTCOME_RETRIEVAL_SEARCH_K=6
OUTCOME_RETRIEVAL_MIN_SIMILARITY=0.58
OUTCOME_RETRIEVAL_FAIL_OPEN=true
```

Empfehlung für Pilotbetrieb: Outcome Retrieval aktivieren, aber Auto-Reply zunächst weiterhin im Shadow-/Dry-Run-Modus beobachten.

`OUTCOME_RETRIEVAL_FAIL_OPEN=true` bedeutet: fällt die Erfahrungs-Suche aus, arbeitet der Agent mit offizieller Knowledge- und sonstiger Evidenz weiter. `false` blockiert die Ticketverarbeitung an dieser Stelle sichtbar. Die Auswahl richtet sich nach dem gewünschten Verfügbarkeits-/Konsistenzprofil.

## Verhalten bei Korrekturen

v1.2.0 führte lokal bereits `supersedes_id`. v1.3.0 zieht die Revision auch in NeuroForge nach:

1. neue korrigierte Outcome-Memory wird gespeichert;
2. Vorgänger wird über seine stabile Outcome Source-ID aufgelöst;
3. Vorgängerstatus wird atomar `superseded`;
4. neue Memory erhält die `Supersedes`-Kante;
5. beide Revisionen bleiben auditierbar;
6. nur die aktive Revision erscheint in künftiger Outcome-Suche.

Es gibt keine destructive Delete-Migration.

## Outcome Retrieval

Der Agent sucht bei einem neuen Ticket zusätzlich in den aktiven menschlich validierten Erfahrungen. Diese Treffer werden ausschließlich in `ContextSnapshot.ValidatedOutcomes` an die Reply-Auswahl übergeben. Die Liste der erlaubten `knowledge_id`-Werte wird weiterhin ausschließlich aus freigegebenen Knowledge-Kandidaten erzeugt.

Damit kann Erfahrung Ranking/Entscheidung unterstützen, ohne einen Policy-Bypass zu erzeugen.

## Quality Replay

Beispieldatensatz kopieren/anpassen:

```bash
cp docs/QUALITY-REPLAY-example.json /tmp/my-cases.json
python3 scripts/quality-replay.py /tmp/my-cases.json \
  --url http://127.0.0.1:8080 \
  --user "$WEB_BASIC_USER" \
  --password "$WEB_BASIC_PASSWORD"
```

Vor einem breiten Auto-Reply-Go-Live sollten historische Tickets mit bekanntem Outcome verwendet werden. Zielwerte müssen organisationsspezifisch definiert und als Release-Gate dokumentiert werden.

## Rollback

Outcome-Retrieval kann ohne Datenmigration deaktiviert werden:

```env
OUTCOME_RETRIEVAL_ENABLED=false
```

Das Outcome-Learning und die bestehenden Memories bleiben erhalten. Für einen vollständigen v1.2-Verhaltensrollback kann zusätzlich der v1.2.0-Code gestartet werden; die neue `superseded`-Statusinformation ist nicht destruktiv.
