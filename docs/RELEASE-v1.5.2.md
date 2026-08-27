# GLPI NeuroForge Mega v1.5.2

v1.5.2 fixes a real autonomous-research failure observed with a narrow FortiClient SSLVPN 7200 goal. SearXNG itself returned relevant Fortinet sources for a focused query, while NeuroForge's deterministic planner sent the complete goal description and target as one oversized query. With a mixed `general,science,it` category configuration this allowed arXiv results to dominate the top-N; the strict relevance gate then correctly rejected every result, leaving the cycle with zero evidence.

## Fixes

- Deterministic goal research no longer sends `title + full description + target` to SearXNG when `autonomy.use_llm=false`.
- The first deterministic query is the compact goal title; an optional second query adds only a small number of non-generic description terms.
- Scheduler/process text remains excluded from all search queries.
- Goal-bound research retries once with SearXNG `category=general` when the configured category mix returns no goal-anchored result.
- The strict pre-fetch relevance gate remains fail-closed; it is not weakened to compensate for bad search ranking.
- Regression coverage reproduces the observed `general,science,it -> unrelated arXiv` result set and verifies that the general fallback returns and learns FortiClient 7200 evidence.

## Validation

All four Go modules pass `go test ./...`, `go vet ./...`, and `go build ./...`. Race checks pass for NeuroForge brain/store/http/research, Agent core/config/knowledge/learning/state, Knowledge staging/store/server, and Control.

The real Docker/SearXNG/Ollama host smoke remains a deployment-host gate.
