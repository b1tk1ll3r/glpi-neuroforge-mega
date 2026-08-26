# Retrieval & Learning Replay Benchmark

v1.3.0 adds a read-only benchmark endpoint: `POST /api/quality/replay`.
It does **not** write to GLPI, does not learn and does not call the answer LLM. It replays
historical ticket text through the current Knowledge retrieval and the human-validated
Outcome retrieval so quality changes can be measured before a rollout.

Each case may specify:

- `query`: historical ticket subject/body snapshot.
- `expected_knowledge_id`: the KB article known to be correct at that time.
- `expected_solution_terms`: terms expected in a technician-validated outcome.
- `k`: evaluation depth (default 10, max 50).

Reported KPIs:

- `knowledge_recall_at_k`
- `knowledge_mrr`
- `outcome_recall_at_k`
- `outcome_mrr`
- `experience_rescued_cases`: cases where the expected KB was not retrieved in K but a
  matching human-validated experience was retrieved. This is a conservative proxy for
  learning lift; it is not counted as auto-reply authority.

Example:

```bash
./scripts/quality-replay.py docs/QUALITY-REPLAY-example.json \
  --url http://127.0.0.1:8080 --user "$WEB_BASIC_USER" --password "$WEB_BASIC_PASSWORD" \
  --output ./data/quality-replay-$(date +%F).json
```

For production acceptance, build a versioned set of historical tickets and require fixed
minimum thresholds before changing retrieval weights, embedding models, HNSW settings or
Outcome retrieval thresholds.
