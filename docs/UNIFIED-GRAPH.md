# Unified Graph Explorer (v1.4.0)

The Control Center remains read-only and now normalizes operational, evidence, learning, research and engineering relationships into one graph contract (`nodes[]`, `edges[]`, bounded metadata).

## Views

- **Runtime & Trust** — services, external systems, scoped credentials and authority boundaries.
- **Ticket Evidence** — ticket, run, KB candidates, validated outcomes, policy checks, model attempts, proposed reply and human decision.
- **Learning Lineage** — accepted/corrected outcomes, NeuroForge memories and immutable `supersedes` chains.
- **Research Provenance** — goal -> query -> source -> claim/evidence -> memory without exposing full source bodies or prompts.
- **NeuroForge Brain** — bounded/redacted memory/synapse/consolidation view; vectors and full memory exports are not returned.
- **Engineering Graph** — reproducible Go AST + root Compose snapshot with components, packages, files, functions, HTTP routes and service dependencies.
- **Change Impact** — bounded bidirectional dependency traversal for a file/symbol/route query with a conservative static risk hint.

## Visualization

The browser uses a dependency-free canvas renderer. 2D is the operational default. 3D is an optional pseudo-perspective explorer for bounded subgraphs. Node budgets and server-side filtering prevent accidental full-graph rendering.

The graph is an explanation/inspection surface, not a decision authority. A `high` change-impact hint does not replace tests, code review or runtime evidence.

## Trust boundaries

The Control Center never receives Agent admin/basic-auth credentials. Agent graph reads require `CONTROL_READ_TOKEN`; NeuroForge graph reads use the existing scoped app key. Graph endpoints are GET-only and return redacted/bounded representations.

## Reproducibility

Regenerate the engineering snapshot after structural code changes:

```sh
make engineering-graph
make engineering-graph-check
```
