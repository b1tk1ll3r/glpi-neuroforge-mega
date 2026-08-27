# Migration v1.5.7 → v1.5.8

v1.5.8 is a non-destructive goal/staging state migration. No volumes must be deleted.

1. Build/publish the v1.5.8 images.
2. Set `IMAGE_TAG=1.5.8`.
3. Pull and recreate NeuroForge and its worker:

```bash
docker compose --profile research pull neuroforge neuroforge-worker
docker compose --profile research up -d --force-recreate neuroforge neuroforge-worker
```

4. Keep all existing goals and volumes.
5. Let each active goal run once. Existing legacy drafts will either be revalidated under the current gate, remain below the current evidence threshold, or receive a current quality error.
6. Article goals should only return to 100% after `staging_draft_validated=true`.
