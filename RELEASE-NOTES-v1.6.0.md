# GLPI NeuroForge Mega v1.6.0

## Durable Master/Subagent Orchestration

- NeuroForge is the authoritative scheduler/master.
- Separate CPU and GPU subagents with capability/resource routing.
- Persistent priorities, dependencies, idempotency, retries/backoff and bounded queues.
- Heartbeat/lease-token fencing; expired leases cannot submit late results.
- Durable `apply_wait` phase for worker results that mutate master state.
- Admin status/job list/retry/cancel and read-only Control Center visibility.

## Knowledge Graph Convergence

- Bounded ANN-assisted graph backfill for imported knowledge memories.
- Multi-neighbor semantic relinking instead of leaving imported chunks isolated.
- Adjacency index, multiple relation labels per edge and bounded multi-hop retrieval.
- Graph health metrics for isolated/linked/multi-linked nodes, degree and connected components.
- Memory-version plus vector-fingerprint fencing rejects stale relink results after delete/recreate races.

## Deployment

- Local `neuroforge-worker-cpu` and `neuroforge-worker-gpu` services.
- `docker-compose.subagent.yml` for additional remote CPU/GPU hosts.
- Non-destructive upgrade from v1.5.9; existing volumes and knowledge remain valid.
