# Migration v1.5.2 → v1.5.3

v1.5.3 is a drop-in quality-gate update. Persistent volumes and existing secrets are retained.

1. Build/publish the v1.5.3 images through the normal Gitea pipeline.
2. Set `IMAGE_TAG=1.5.3`.
3. Run `docker compose --profile research pull`.
4. Recreate NeuroForge and its worker with `docker compose --profile research up -d --force-recreate neuroforge neuroforge-worker`.
5. Remove/reject any previously generated staging draft whose sources are not actually relevant to the goal. The upgrade intentionally does not delete human-review artifacts automatically.
6. If duplicate active goals already exist, keep the desired goal and delete the duplicate through the UI/API. New identical active goals are rejected with HTTP 409.
7. Re-run the narrow FortiClient SSLVPN 7200 test. Sources that merely contain `sslvpn`, or a longer identifier containing `7200` as a substring, must be rejected before evidence ingestion.

Expected audit behavior: staging `research_evidence` and `research_sources` match the actual `research_evidence_ids` / `research_source_uris` used for the draft; aggregate goal totals are available in the new `research_goal_*` fields.
