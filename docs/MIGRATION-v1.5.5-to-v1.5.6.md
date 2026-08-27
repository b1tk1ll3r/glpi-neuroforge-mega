# Migration v1.5.5 → v1.5.6

v1.5.6 is a drop-in structured-output hardening release. No storage migration is required. Existing goals, memories, synapses, research history and staging drafts remain intact.

1. Build/publish the v1.5.6 images through the normal Gitea pipeline.
2. Set `IMAGE_TAG=1.5.6`.
3. Pull and recreate NeuroForge and its worker:

```bash
docker compose --profile research pull neuroforge neuroforge-worker
docker compose --profile research up -d --force-recreate neuroforge neuroforge-worker
```

4. Do not delete volumes.
5. Re-run the previously blocked goal. Existing evidence can be reused; a reset is not necessary.

There are no new mandatory environment variables. Ollama JSON mode is selected internally only for structured-output calls.
