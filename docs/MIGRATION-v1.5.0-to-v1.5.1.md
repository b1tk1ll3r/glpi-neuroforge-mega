# Migration v1.5.0 → v1.5.1

v1.5.1 is a stop-the-line research/staging quality fix. It is intended to be a drop-in image/config upgrade from v1.5.0; persistent volumes are retained.

## Required environment

Keep the existing v1.5.0 secrets and add or confirm:

```env
IMAGE_TAG=1.5.1
NEUROFORGE_KB_STAGING_SYNTHESIS_MODE=llm
```

For an external Ollama host, v1.5.1 again honors:

```env
OLLAMA_BASE_URL=http://your-ollama:11434
OLLAMA_URLS=http://your-ollama:11434
```

If these variables are omitted, the root Compose defaults to the internal `http://ollama:11434` service.

## Behaviour changes

- Goal research rejects search/page material that has no subject anchor overlap with the goal before it is learned.
- Research memories persist `provenance.goal_id`; legacy `goal:<id>` tags remain supported for reconciliation.
- Goal evidence/source counters are recomputed from relevant persisted evidence, so counters can decrease after upgrade when old off-topic evidence is removed from the goal view.
- Draft evidence is diversified across independent sources and limited to at most two chunks per source after the diversity pass.
- Production staging uses LLM synthesis even when `autonomy.use_llm=false`. Raw evidence concatenation is no longer the default article path.
- Failed, empty or off-topic synthesis is fail-closed: no staging article is created/updated.
- Targets containing `Artikel`/`article` count created staging drafts instead of evidence chunks.
- The standalone NeuroForge worker Compose no longer passes its token as a CLI argument.

## Existing bad staging drafts

Existing drafts are not automatically promoted or deleted. For a bad draft created by v1.5.0, leave it in staging or delete it manually. The next successful cycle for the same goal can update the same integration key with a newly synthesized draft.

## Upgrade

```sh
docker compose --profile research pull
docker compose --profile research up -d --force-recreate
```

Do not use `down -v`; the persistent NeuroForge/Agent/Ollama volumes must be retained.

After startup, verify `/readyz`, then inspect the goal counters. Off-topic historical evidence may disappear from the per-goal counts by design.
