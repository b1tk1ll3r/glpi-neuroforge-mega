# Environment configuration (v1.4.x)

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
- `NEUROFORGE_API_KEY` is derived from `NEUROFORGE_APP_API_KEY`
- Brain-activity endpoints are wired internally by Compose

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
