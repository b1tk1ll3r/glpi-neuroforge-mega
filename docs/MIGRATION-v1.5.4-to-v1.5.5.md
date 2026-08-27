# Migration v1.5.4 → v1.5.5

v1.5.5 is a drop-in quality-hardening update. Existing NeuroForge/Agent/Knowledge volumes, goals, research evidence and staging drafts are retained.

1. Build/publish the v1.5.5 images through the normal Gitea pipeline.
2. Set `IMAGE_TAG=1.5.5`.
3. Keep the new production defaults enabled unless you intentionally run a diagnostic environment:

```env
NEUROFORGE_KB_STAGING_REQUIRE_AUTHORITATIVE_SOURCE=true
NEUROFORGE_KB_STAGING_MIN_AUTHORITATIVE_SOURCES=1
NEUROFORGE_KB_STAGING_AUTHORITATIVE_DOMAINS=
NEUROFORGE_KB_STAGING_VERIFY_CLAIMS=true
NEUROFORGE_KB_STAGING_MIN_CLAIM_COVERAGE=1.0
NEUROFORGE_KB_STAGING_REQUIRE_AUTHORITATIVE_ACTIONS=true
NEUROFORGE_KB_STAGING_MAX_VERIFICATION_STATEMENTS=24
NEUROFORGE_KB_STAGING_VERIFICATION_REPAIR=true
```

4. Pull/recreate NeuroForge and the worker (or the complete stack):

```bash
docker compose --profile research pull
docker compose --profile research up -d --force-recreate neuroforge neuroforge-worker
```

5. Do not delete volumes. Existing low-quality staging drafts created by older releases should be reviewed/deleted manually; v1.5.5 does not silently rewrite previously stored draft content.

## Expected behavioral change

A goal may now remain at `0/1 Staging-Artikel` even with many learned web memories if the selected bundle has no authoritative source or if the verifier finds unsupported/contradicted claims. That is the intended fail-closed production behavior. The goal's `last_staging_error` explains the blocked gate.
