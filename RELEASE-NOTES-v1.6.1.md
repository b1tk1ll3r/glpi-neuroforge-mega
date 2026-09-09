# GLPI NeuroForge Mega v1.6.1

## Production hotfix: Master crash / silent recovery on large Knowledge corpora

v1.6.1 keeps the v1.6.0 Master/Subagent and n:m Knowledge-Graph architecture and hardens the NeuroForge master for long-running bulk imports/backfills.

### Fixed

- Completed `vector.relink` jobs no longer retain full target/candidate vectors after successful master apply.
- A one-time streaming startup migration compacts large v1.6.0 `state.json` checkpoints before full JSON unmarshal.
- Durable pending-job payload bytes are bounded by `NEUROFORGE_WORKER_MAX_QUEUED_PAYLOAD_MB` (default 128 MiB).
- HNSW segmented checkpoint deltas no longer deep-copy every vector/node on each checkpoint; only changed nodes are copied.
- Checkpoint ordering is index-first, then `state.json`, then WAL prune, improving crash recovery when index persistence is interrupted.
- Startup with explicit `-listen` exposes `/livez` and a minimal `/admin` recovery page before the store is fully open.
- Startup phases are logged, so segment/WAL/HNSW recovery is no longer silent.
- Existing v1.6.0 completed relink payloads are compacted on first v1.6.1 boot.
- Authoritative `state.json` and `secrets.json` are decoded directly from files instead of `ReadFile`+`Unmarshal`; corrupt or trailing JSON now fails closed with an explicit startup error instead of being silently ignored.
- Checkpoints/secrets/index metadata are JSON-streamed to atomic temporary files instead of allocating a second complete encoded byte slice; the final file and directory entry are synced before success.
- WAL recovery immediately drops transient payload/result blobs from already-completed `vector.relink` jobs, so a large surviving WAL cannot rebuild the same multi-GB terminal-job heap during restart.
- Fresh data directories no longer consume the one-time v1.6.1 compaction marker before a possible v1.6.0 restore.

### Safer graph defaults

- `NEUROFORGE_WORKER_JOB_RETENTION_HOURS=24`
- `NEUROFORGE_WORKER_MAX_TERMINAL_JOBS=2000`
- `NEUROFORGE_GRAPH_BACKFILL_BATCH_SIZE=16`
- `NEUROFORGE_GRAPH_BACKFILL_MAX_QUEUED=64`
- `NEUROFORGE_WORKER_MAX_QUEUED_PAYLOAD_MB=128`

These are throughput/stability defaults; completed relink payload compaction is enforced independently of the values above.
