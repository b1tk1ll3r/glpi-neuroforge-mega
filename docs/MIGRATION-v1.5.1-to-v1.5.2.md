# Migration v1.5.1 → v1.5.2

v1.5.2 is a drop-in research-planning fix. Persistent volumes and existing secrets are retained.

1. Build/publish the v1.5.2 images through the normal Gitea pipeline.
2. Set `IMAGE_TAG=1.5.2`.
3. `docker compose --profile research pull`
4. `docker compose --profile research up -d --force-recreate neuroforge neuroforge-worker`
5. Re-run the FortiClient SSLVPN 7200 goal and inspect `/api/v1/goals/{id}/research/live`.

Expected behavior: `query.planned` starts with `FortiClient SSLVPN 7200`; if the configured category mix produces no relevant result, `search.fallback` is emitted and a `general` search is used. Relevant Fortinet hits should proceed to `download.started` rather than being rejected before fetch.
