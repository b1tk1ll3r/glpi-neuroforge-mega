# Migration v0.2.x -> v0.3.0

1. Server stoppen und das bisherige Datenverzeichnis sichern.
2. v0.3.0 mit **demselben** `-data`-Verzeichnis starten.
3. Der alte `state.json` wird geladen. Neue Config-Felder erhalten sichere Defaults.
4. Bestehende Memories erhalten bei Bedarf automatisch `status=active`, `version=1` und `home_shard_id=<local shard>`.
5. Beim ersten Start werden `wal/` und `hnsw.snapshot.json` angelegt und ein v0.3-Checkpoint geschrieben.

Es ist keine manuelle Datenkonvertierung nötig.

## Neue Defaults mit absichtlicher Sicherheitswirkung

- `autonomy.enabled=false`
- `rebalancing.enabled=false`
- `rebalancing.mode=replicate`
- `openai.enabled` bleibt unverändert bzw. im Default `false`
- `storage.wal_sync=true` für neue/vollständig migrierte Storage-Konfigurationen
- Retention ist aktiviert, greift aber standardmäßig erst nach 30 Tagen und löscht konsolidierte Episoden nicht automatisch.

Vor `rebalancing.mode=move` zuerst einen Dry-Run und danach `replicate` in der Zielumgebung testen.
