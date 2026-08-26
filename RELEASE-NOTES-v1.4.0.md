# GLPI NeuroForge Mega v1.4.0

## Unified Graph Explorer

- read-only graph explorer in Control Center with 2D/3D modes, filters, inspector and bounded node budgets
- Ticket Evidence graph including KB candidates, validated outcomes, policy gates, model attempts, proposed answer and human result
- Learning Lineage with accepted/corrected outcomes and immutable supersession chains
- Research Provenance from goal/query/source/evidence to learned memory
- bounded/redacted NeuroForge Brain graph
- reproducible Engineering Graph generated from Go AST plus Docker Compose topology
- Change Impact / blast-radius view for files, symbols and routes
- optional developer-only Codebase Memory MCP link/integration; no production dependency

## Security and control

- new dedicated `CONTROL_READ_TOKEN` for Agent graph reads; no Agent admin credentials are given to Control Center
- NeuroForge graph endpoints remain app-key scoped and omit vectors/full source bodies
- server-side graph budgets and progressive filtering protect browser/runtime resources
- Codebase Memory remains optional and cannot affect platform readiness

## Operations

- `make engineering-graph` and `make engineering-graph-check`
- `scripts/codebase-memory-ui.sh` for optional local developer analysis
- `.cbmignore` included
