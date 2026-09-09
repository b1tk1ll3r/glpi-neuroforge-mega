# Migration v1.6.1 -> v1.6.2

No data-volume migration is required. Keep `neuroforge-data`, Agent data and Knowledge directories intact.

1. Set `IMAGE_TAG=1.6.2`.
2. Copy the new optional Ollama variables into your `.env`:

   ```env
   OLLAMA_API_KEY=
   NEUROFORGE_OLLAMA_API_KEY=
   NEUROFORGE_WORKER_OLLAMA_API_KEY=
   ```

   Leave them empty for an unauthenticated native Ollama endpoint.
3. If Ollama is protected by a Bearer-aware proxy, normally set only `OLLAMA_API_KEY`. Use the two NeuroForge overrides only when the Master and model worker need different credentials.
4. Pull/recreate services without deleting volumes:

   ```bash
   docker compose --profile research pull
   docker compose --profile research up -d --force-recreate --remove-orphans
   ```
5. For distributed deployments, use the complete `.env` shipped under `deployments/master`, `deployments/cpu-subagent`, and `deployments/gpu-subagent` and preserve the same `NEUROFORGE_WORKER_TOKEN` on Master and subagents.

The v1.6.1 OOM/recovery fixes are part of v1.6.2 and must not be removed when merging older deployment files.
