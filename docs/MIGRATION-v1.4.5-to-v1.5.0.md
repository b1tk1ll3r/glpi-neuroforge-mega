# Migration v1.4.5 → v1.5.0

## 1. Back up v1.4.5 data

Take an existing host backup before replacing the checkout. For future v1.5.0 backups use `scripts/backup-data.sh`.

## 2. Replace/update configuration

Start from the v1.5.0 `.env.example`; do not simply keep the old broad Compose environment behavior. Preserve reviewed Agent/GLPI policy values and add:

```env
IMAGE_TAG=1.5.0
NEUROFORGE_INTEGRATION_TOKEN=<unique 24+ char secret>
NEUROFORGE_CONTROL_READ_TOKEN=<unique 24+ char secret>
CONTROL_BASIC_AUTH_USER=admin
CONTROL_BASIC_AUTH_PASSWORD=<unique 12+ char password>
NEUROFORGE_READINESS_OLLAMA_LIVE=true
AGENT_LEGACY_KNOWLEDGE_EDITOR_ENABLED=false
```

Keep the pre-existing Admin/App/Worker/Metrics, Knowledge integration and Agent Control-Read tokens unique. `./scripts/generate-secrets.sh` emits all required new secret values.

## 3. Understand credential remapping

- Agent `NEUROFORGE_API_KEY` and Brain Activity use `NEUROFORGE_INTEGRATION_TOKEN` internally.
- Control Center uses `NEUROFORGE_CONTROL_READ_TOKEN` for NeuroForge and `CONTROL_READ_TOKEN` for Agent read-only endpoints.
- The general `NEUROFORGE_APP_API_KEY` no longer authorizes `/api/v1/integrations/*` writes.

## 4. Deploy registry images only

```sh
./scripts/preflight.sh
docker compose pull
docker compose up -d --remove-orphans
```

Do not run a production source build. Local development uses:

```sh
docker compose -f docker-compose.yml -f docker-compose.dev.yml up --build
```

## 5. Verify before enabling GLPI writes

Follow every gate in `docs/GO-LIVE-v1.5.0.md`. In particular verify both Ollama models, Research→Staging with goal-summary learning disabled, duplicate-cycle `409`, promotion, degraded NeuroForge behavior, and a complete `DRY_RUN=true` GLPI ticket flow.
