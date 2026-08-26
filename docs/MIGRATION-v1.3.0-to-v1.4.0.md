# Migration v1.3.0 -> v1.4.0

1. Generate and add a new `CONTROL_READ_TOKEN` (minimum 24 characters) to `.env`.
2. Recreate `agent` and `control`; no data migration is required.
3. Open the Control Center and verify Runtime, Ticket, Learning, Research, Brain and Engineering graph views.
4. Keep Codebase Memory variables empty unless the optional developer tool is installed.
5. After code changes regenerate `services/control/engineering-graph.json` with `make engineering-graph`.

Rollback: deploy v1.3.0 again. The new graph APIs are read-only and introduce no persistent schema change.
