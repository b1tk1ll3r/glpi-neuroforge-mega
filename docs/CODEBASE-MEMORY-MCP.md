# Optional Codebase Memory MCP integration

`codebase-memory-mcp` is an optional developer tool, not a production dependency and not an authoritative NeuroForge store. The project uses its structural-code-graph ideas while retaining an in-repo Go AST/Compose snapshot for reproducibility.

## Why optional

The external tool can provide deeper MCP/Cypher/code-navigation and its own rich graph UI. The Mega project's runtime, GLPI decisions, learning and Control Center do not depend on it.

## Local use

Install `codebase-memory-mcp` according to the upstream project, then run:

```sh
./scripts/codebase-memory-ui.sh
```

The helper sets `CBM_ALLOWED_ROOT` to this repository, indexes it through the upstream CLI and starts the optional UI (default port 9749). `.cbmignore` keeps generated/runtime data out of indexing.

To expose its status/link in the Control Center set, as appropriate for your host/network:

```env
CODEBASE_MEMORY_URL=http://host.docker.internal:9749
PUBLIC_CODEBASE_MEMORY_URL=http://localhost:9749
```

Leave `CODEBASE_MEMORY_URL` empty when the Control container should not health-check the developer service. The component is always optional and never affects platform readiness.
