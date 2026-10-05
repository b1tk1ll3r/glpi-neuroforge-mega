# Migration v1.6.2 -> next release

Applies to the first release built after v1.6.2 (version number not yet assigned).

## Knowledge runs as non-root (UID/GID 65532)

The Knowledge image no longer runs as root. Its data directories (`KB_DATA_PATH`, `KB_STAGING_PATH`, `KB_BACKUP_PATH`, or `runtime/{knowledge,staging,backups}` in the split deployments) must be owned by UID/GID `65532`, the same runtime user as the Agent.

Every compose file that starts Knowledge now contains a one-shot service `knowledge-data-init`. It uses the same Knowledge image, runs once as root with only `CHOWN`, `FOWNER` and `DAC_READ_SEARCH`, has no network, and changes the ownership of the writable mounts before `knowledge` starts. It skips read-only mounts (`KB_DATA_MOUNT_MODE=ro`); Knowledge only needs read access there, and the files are created world-readable (`0644`/`0755`).

1. Set the new `IMAGE_TAG`.
2. Pull/recreate services without deleting data. `knowledge-data-init` runs automatically before `knowledge`:

   ```bash
   docker compose --profile research pull
   docker compose --profile research up -d --force-recreate --remove-orphans
   ```

3. Check that the init container finished successfully:

   ```bash
   docker compose logs knowledge-data-init
   ```

**Host-side consequences**

- After the upgrade the Knowledge directories on the host belong to UID `65532`. Editing knowledge JSON directly on the host now requires `sudo` (or a group/ACL you add yourself). Editing through the Knowledge web UI is unaffected.
- `scripts/backup-data.sh` keeps working because the files stay world-readable. After `scripts/restore-data.sh`, the next `docker compose up` corrects the ownership again.
- If you use your own compose file, add an equivalent init step or run once:

  ```bash
  sudo chown -R 65532:65532 ./knowledge ./staging ./backups
  ```

## Pinned third-party images

`ollama` and `searxng` in the root `docker-compose.yml` (and the SearXNG default in `deployments/master`) are pinned by digest instead of `latest`. `.env.example` now contains the SearXNG digest. If your `.env` still sets `SEARXNG_IMAGE=docker.io/searxng/searxng:latest`, replace it with the digest from `.env.example`. You can override Ollama with `OLLAMA_IMAGE`.

## Healthchecks

`knowledge` and `control` now have container healthchecks (`/api/health` and `/healthz`). No configuration is needed.
