# Migration v1.6.0 → v1.6.1

1. **Kein Volume löschen.** `neuroforge-data` enthält den autoritativen Store.
2. Backup des NeuroForge-Volumes erstellen.
3. `IMAGE_TAG=1.6.1` setzen.
4. Für große Graph-Backfills die neuen sicheren Defaults übernehmen:
   - `NEUROFORGE_WORKER_MAX_QUEUED_PAYLOAD_MB=128`
   - `NEUROFORGE_WORKER_JOB_RETENTION_HOURS=24`
   - `NEUROFORGE_WORKER_MAX_TERMINAL_JOBS=2000`
   - `NEUROFORGE_GRAPH_BACKFILL_BATCH_SIZE=16`
   - `NEUROFORGE_GRAPH_BACKFILL_MAX_QUEUED=64`
5. Nur NeuroForge Master zuerst neu erstellen. Beim ersten Start läuft ggf. `checkpoint.precompact`; diese Streaming-Migration entfernt historische abgeschlossene Relink-Vectorblobs aus dem v1.6.0-Checkpoint.
6. Während Recovery sind `/livez` und `/admin` bereits erreichbar; `/readyz` bleibt bis zum vollständigen Store-/Provider-Start 503.
7. Erst nach `/readyz=200` CPU-/GPU-Subagents und übrige Services normal starten.

Erwartete Startup-Phasen im Log:

`store.prepare → checkpoint.precompact → checkpoint.load → memory-segments.scan → wal.replay → jobs.compact → graph.restore → disk-ann.load → hnsw.snapshot.load [→ hnsw.rebuild] → checkpoint.write → store.ready`

## Recovery-Verhalten in v1.6.1

- `state.json` und `secrets.json` sind autoritativ. Syntaxfehler, ein zweites angehängtes JSON-Objekt oder sonstige Dekodierfehler werden **nicht** mehr ignoriert; NeuroForge meldet den konkreten Startup-Fehler und bleibt nicht mit stillschweigend zurückgesetztem Zustand/Tokens in Betrieb.
- Große Checkpoints werden direkt aus der Datei dekodiert und beim Schreiben gestreamt. Dadurch entfällt die zusätzliche vollständige `[]byte`-Kopie des Checkpoints im RAM.
- Beim WAL-Replay werden bereits abgeschlossene `vector.relink`-Payloads sofort verworfen. `apply_wait`, `queued`, `claimed`, `retry_wait` und fehlgeschlagene/retrybare Jobs behalten ihre für Recovery/Retry benötigten Daten.
- Ein beschädigter `state.json`/`secrets.json` wird nicht automatisch überschrieben. Erst Backup/Restore bzw. gezielte Reparatur durchführen.

Vor einem Upgrade bei einem bereits hängenden v1.6.0-Master das Volume **nicht** löschen. Ein Dateisystem-/Volume-Snapshot ist vorzuziehen.
