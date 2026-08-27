# Migration v1.5.3 → v1.5.4

v1.5.4 is a drop-in NeuroForge staging-synthesis robustness update. Persistent volumes, existing goals, evidence, drafts, and secrets are retained.

1. Build/publish the v1.5.4 images through the normal Gitea pipeline.
2. Set `IMAGE_TAG=1.5.4`.
3. Run `docker compose --profile research pull`.
4. Recreate at least `neuroforge` and `neuroforge-worker`; no volume reset is required.
5. Leave the existing failed goal active. Its next autonomous cycle can retry staging from the persisted relevant evidence.
