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
```

`NEUROFORGE_KB_STAGING_URL` and `NEUROFORGE_KB_STAGING_TOKEN` are container-internal values owned by the root Compose file. The token is derived from the existing `KB_INTEGRATION_TOKEN`; do not duplicate it under a second operator-managed secret name.

This bridge can only create/update **human-review staging**. The Knowledge service enforces `auto_reply=false` and does not expose production promotion through this integration token.

## Production secret isolation

The production Compose does not use `env_file`. Agent and Knowledge receive only explicit runtime variables. NeuroForge Admin/Worker/Metrics, Knowledge editor and Control Center credentials are therefore not broadly inherited by unrelated containers. Local source builds use the separate `docker-compose.dev.yml` override.
