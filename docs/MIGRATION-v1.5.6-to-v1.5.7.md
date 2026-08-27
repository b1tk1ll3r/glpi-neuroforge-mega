# Migration v1.5.6 → v1.5.7

v1.5.7 is a drop-in identifier-grounding fix. No storage migration is required and no new environment variable is mandatory. Existing goals, memories, synapses, research history and staging drafts remain intact.

1. Build/publish the v1.5.7 images through the normal Gitea pipeline.
2. Set `IMAGE_TAG=1.5.7`.
3. Pull and recreate NeuroForge and its worker:

```bash
docker compose --profile research pull neuroforge neuroforge-worker
docker compose --profile research up -d --force-recreate neuroforge neuroforge-worker
```

4. Do not delete volumes.
5. Let the previously blocked goals run again. Their existing evidence can be reused.
