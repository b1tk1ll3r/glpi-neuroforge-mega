# Environment configuration (v1.5.0)

The repository-level `.env.example` is the canonical configuration template for the Mega stack.
It intentionally includes the complete GLPI Agent configuration plus NeuroForge, controlled-learning,
Knowledge Editor, Control Center and optional Research/SearXNG settings.

## Important migration rule

Do not copy the old standalone agent `.env` unchanged into the Mega stack without reviewing it.
The old variables are still supported, but container-internal values are now owned by Compose:

- `HTTP_ADDR=:8080`
- `DATA_DIR=/app/data`
- `KNOWLEDGE_DIR=/app/knowledge`
- `OLLAMA_URL=http://ollama:11434`
- `NEUROFORGE_URL=http://neuroforge:8080`
- `NEUROFORGE_API_KEY` is derived from `NEUROFORGE_INTEGRATION_TOKEN`
- Brain-activity endpoints are wired internally by Compose and use `NEUROFORGE_INTEGRATION_TOKEN`

The host-facing ports are configured separately with `AGENT_HOST_PORT`, `KNOWLEDGE_HOST_PORT`,
`CONTROL_HOST_PORT`, `NEUROFORGE_HOST_PORT`, `OLLAMA_HOST_PORT` and `SEARXNG_HOST_PORT`.

## First setup

```sh
cp .env.example .env
./scripts/generate-secrets.sh
```

Copy the generated values into `.env`, then configure the required GLPI credentials:

- `GLPI_URL`
- `GLPI_CLIENT_ID`
- `GLPI_CLIENT_SECRET`
- `GLPI_USERNAME`
- `GLPI_PASSWORD`
- `GLPI_AGENT_USER_ID` before enabling Auto Reply / escalation writes

Keep `DRY_RUN=true`, `AUTO_REPLY=false`, `AUTO_PRIORITY=false` and `AUTO_ESCALATION=false`
for the first integration tests.

## Legacy behavior that is no longer represented by defaults

A migration from an older agent `.env` can materially change behavior if only the short Mega template
is used. In particular review:

- `GLPI_KB_ENABLED`
- `GLPI_KB_AUTO_REPLY`
- `GLPI_KB_AUTO_REPLY_CATEGORY_IDS`
- `KNOWLEDGE_ALLOWED_SOURCES`
- `KNOWLEDGE_AUTO_REPLY_SOURCES`
- `KNOWLEDGE_WEB_EDIT_ENABLED`
- `CATEGORY_CONFIDENCE`
- `REPLY_CONFIDENCE`
- Context / Change / Uptime Kuma options
- Priority and escalation policy
- Communication policy

The canonical `.env.example` now contains these settings explicitly.

## Secrets

Never commit `.env`. The tracked file must remain `.env.example` only.
If credentials were pasted into issue trackers, chats, CI logs, shell history or screenshots,
rotate them before production use.

## Research → Knowledge Staging (v1.5.0+)

The autonomous research bridge is controlled independently from Research and Goal Learning:

```env
NEUROFORGE_KB_STAGING_ENABLED=true
NEUROFORGE_KB_STAGING_MIN_EVIDENCE=4
NEUROFORGE_KB_STAGING_MIN_SOURCES=2
NEUROFORGE_KB_STAGING_MIN_CORROBORATIONS=0
NEUROFORGE_KB_STAGING_MAX_EVIDENCE=12
NEUROFORGE_KB_STAGING_SYNTHESIS_MODE=llm
NEUROFORGE_KB_STAGING_REQUIRE_AUTHORITATIVE_SOURCE=true
NEUROFORGE_KB_STAGING_MIN_AUTHORITATIVE_SOURCES=1
NEUROFORGE_KB_STAGING_AUTHORITATIVE_DOMAINS=
NEUROFORGE_KB_STAGING_VERIFY_CLAIMS=true
NEUROFORGE_KB_STAGING_MIN_CLAIM_COVERAGE=1.0
NEUROFORGE_KB_STAGING_REQUIRE_AUTHORITATIVE_ACTIONS=true
NEUROFORGE_KB_STAGING_MAX_VERIFICATION_STATEMENTS=32
NEUROFORGE_KB_STAGING_VERIFICATION_REPAIR=true
NEUROFORGE_KB_STAGING_SYNTHESIS_MAX_TOKENS=2600
NEUROFORGE_KB_STAGING_EVIDENCE_PROMPT_MAX_CHARS=14000
NEUROFORGE_KB_STAGING_MIN_ARTICLE_CHARS=3500
NEUROFORGE_KB_STAGING_TARGET_ARTICLE_CHARS=6500
NEUROFORGE_KB_STAGING_MAX_ARTICLE_CHARS=10000
NEUROFORGE_KB_STAGING_MIN_ANSWER_CHARS=160
NEUROFORGE_KB_STAGING_MAX_ANSWER_CHARS=1200
```

`NEUROFORGE_KB_STAGING_URL` and `NEUROFORGE_KB_STAGING_TOKEN` are container-internal values owned by the root Compose file. The token is derived from the existing `KB_INTEGRATION_TOKEN`; do not duplicate it under a second operator-managed secret name.

This bridge can only create/update **human-review staging**. The Knowledge service enforces `auto_reply=false` and does not expose production promotion through this integration token.

## Production secret isolation

The production Compose does not use `env_file`. Agent and Knowledge receive only explicit runtime variables. NeuroForge Admin/Worker/Metrics, Knowledge editor and Control Center credentials are therefore not broadly inherited by unrelated containers. Local source builds use the separate `docker-compose.dev.yml` override.

### Staging quality gate (v1.5.1)

`NEUROFORGE_KB_STAGING_SYNTHESIS_MODE=llm` is the production default. Drafts are published only after goal-relevance filtering, source diversification and successful LLM synthesis. `evidence` is a diagnostic mode that exposes the selected evidence bundle and must not be treated as an article. Off-topic search results are rejected before ingestion and do not count toward goal progress.

### Production grounding gate (v1.5.5)

Before an LLM-synthesized research article reaches human-review staging, NeuroForge now ranks selected evidence by source authority, requires the configured number of first-party/authoritative sources, validates critical identifiers (error codes, CVEs, KB IDs, versions and command switches) against the source bundle, and runs a second claim-verification pass. Every material draft statement must be accounted for. Prescriptive guidance must cite at least one evidence item classified as authoritative when `NEUROFORGE_KB_STAGING_REQUIRE_AUTHORITATIVE_ACTIONS=true`.

The verifier is fail-closed. Unsupported or contradicted statements prevent publication. With `NEUROFORGE_KB_STAGING_VERIFICATION_REPAIR=true`, one evidence-only rewrite is attempted and the complete draft is then verified again. The repair may remove unsupported content but may not add outside knowledge. Source authority, claim-level evidence IDs, coverage, contradictions and whether a repair occurred are persisted in the staging JSON for human audit.

Microsoft Q&A (`learn.microsoft.com/.../answers/...`) is intentionally treated as vendor-community rather than primary documentation. Operator-specific first-party domains can be added with `NEUROFORGE_KB_STAGING_AUTHORITATIVE_DOMAINS`.

### Article depth gate (v1.5.9)

`text` is the canonical full knowledge article. `answer` is intentionally a shorter operational summary for downstream reply use. Production defaults require the full article to contain at least 3,500 characters and target about 6,500 characters without exceeding 10,000. If the first grounded synthesis is shorter or otherwise outside the configured bounds, NeuroForge performs one evidence-only article-depth rewrite. It may reorganize and expand supported detail but may not add outside knowledge or filler. The rewritten draft is then identifier- and claim-verified again.

The synthesis call, syntax repair and grounding rewrite use `NEUROFORGE_KB_STAGING_SYNTHESIS_MAX_TOKENS` rather than the old hard-coded 1,200/1,400 token limits. `NEUROFORGE_KB_STAGING_EVIDENCE_PROMPT_MAX_CHARS` fairly budgets evidence text across selected sources so the local-model context still has room for a full article. With the Mega Compose, keep `NEUROFORGE_OLLAMA_NUM_PREDICT=0` so the call-specific staging budget is not overridden by a lower persisted Ollama value.

Claim verification checks all material article statements in batches of `NEUROFORGE_KB_STAGING_MAX_VERIFICATION_STATEMENTS`; the value is a batch size, not a total verification cap. A hard safety ceiling of 128 material statements remains. Staging persists `article_quality` with actual article/answer lengths, configured bounds, evidence count, prompt size, expansion status and synthesis token usage.

Fortinet `support-forum` pages are treated as vendor-community evidence rather than authoritative primary documentation. Editorial `technical-tip` and `troubleshooting-tip` pages remain eligible as authoritative first-party material.

### Master/Subagent Orchestrator & Knowledge Graph (v1.6.0)

The master scheduler is controlled by `NEUROFORGE_WORKER_LEASE_SECONDS`, `NEUROFORGE_WORKER_HEARTBEAT_SECONDS`, `NEUROFORGE_WORKER_STALE_AFTER_SECONDS`, retry/backoff/queue/retention settings, and the graph-backfill limits shown in `.env.example`. Local workers use `NEUROFORGE_CPU_WORKER_CONCURRENCY` / `NEUROFORGE_GPU_WORKER_CONCURRENCY`; remote workers use `docker-compose.subagent.yml` with a reachable `NEUROFORGE_MASTER_URL` and unique `NEUROFORGE_CPU_WORKER_ID` / `NEUROFORGE_GPU_WORKER_ID`.

`NEUROFORGE_OFFLOAD_CHAT` and `NEUROFORGE_OFFLOAD_EMBEDDINGS` prefer a live capability-compatible GPU subagent. If no compatible live subagent is available, NeuroForge continues through its normal provider router. Graph convergence is bounded by `NEUROFORGE_GRAPH_BACKFILL_BATCH_SIZE`, `NEUROFORGE_GRAPH_BACKFILL_MAX_QUEUED`, `NEUROFORGE_GRAPH_BACKFILL_MIN_DEGREE`, `NEUROFORGE_GRAPH_CANDIDATE_MULTIPLIER` and the retry cooldown. Multi-hop retrieval is bounded by `NEUROFORGE_GRAPH_MAX_HOPS`, `NEUROFORGE_GRAPH_HOP_DECAY`, `NEUROFORGE_GRAPH_MAX_EXPANSION` and `NEUROFORGE_GRAPH_MIN_EDGE_WEIGHT`.
