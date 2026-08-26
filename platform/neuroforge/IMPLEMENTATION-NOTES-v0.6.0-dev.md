# NeuroForge v0.6.0-dev – Sequential PQ segment scan

Status: **implemented, intentionally not built/tested/benchmarked** per request.

## Change

The v0.5 -> v0.6 disk-PQ migration fallback no longer performs one random `ReadAt`/segment lookup per memory during the full encode pass.

### Segment scanner

`internal/store/segment.go` now contains a forward-only live-record scanner:

- captures a point-in-time snapshot of the latest live segment locations;
- groups/sorts locations by segment and physical offset;
- opens each segment once;
- consumes bytes strictly forward through a 1 MiB buffered reader;
- reuses the payload buffer across records;
- remains safe with concurrent appends because segment files are append-only and the captured offsets do not move.

### Vector-only decode path

`IterateLiveVectorsSequential(dim, callback)` decodes only the fields needed by disk ANN:

- top-level record ID/op;
- memory status;
- vector dimension;
- vector values.

Memory text, tags, session metadata, timestamps and other unrelated fields are skipped by `encoding/json` instead of being materialized into a full `core.Memory`.

### PQ builder integration

`internal/store/diskann.go` uses the new vector-only sequential iterator for both:

1. training-sample collection on a v0.5 migration store; and
2. the complete IVF-PQ encode pass.

While the first pass seeds the compact vector journal, only `ID + Vector` are buffered, not the full Memory body.

## Durability / authority

The memory segments remain the authoritative source. The vector journal remains a rebuildable acceleration cache. This change does not alter the memory segment format.

## Validation status

The sequential PQ migration path remains intentionally unbenchmarked and was not directly exercised after its implementation. For the later observability addition only the targeted `internal/httpapi` tests plus dashboard JavaScript/OpenAPI syntax validation were run; no PQ benchmark or migration benchmark was started.

## Admin Dashboard / Prometheus

- `/admin` serves the embedded administration UI; `/` remains compatible.
- The Observability tab renders client-side rolling charts for heap, request rate/p95 and memory count.
- `GET /metrics` exports Prometheus text exposition `0.0.4` and requires `Authorization: Bearer <metrics_token>` (admin token accepted as bearer fallback).
- `metrics_token` is generated at first startup and can be overridden with `NEUROFORGE_METRICS_TOKEN`.
- HTTP metrics use the matched Go `ServeMux` pattern as the route label, preventing dynamic memory/goal IDs from exploding cardinality.
- Store observability is collected through `ObservabilitySnapshot`, which does not iterate over the complete memory catalog.
- The periodic dashboard refresh no longer performs remote provider health checks; those remain an explicit admin action.


## Admin model routing

- The internal `routing.chat_provider` / `routing.embedding_provider` and per-Ollama `chat_model` / `embedding_model` settings are now exposed in a dedicated **Modelle & Routing** admin view.
- `GET/PUT /admin/api/model-routing` supports partial updates; a body containing only `routing` and `ollama` is sufficient.
- `routing.chat_node_id` and `routing.embedding_node_id` optionally pin core routes to one Ollama node.
- `routing.critic`, `routing.consolidator`, and `routing.goal` are role routes with provider/model/node_id.
- Explicit Ollama node pins are strict; unpinned routes retain weighted multi-Ollama failover.
- Provider health now parses `/api/tags` and reports model names without generating text or embeddings.
