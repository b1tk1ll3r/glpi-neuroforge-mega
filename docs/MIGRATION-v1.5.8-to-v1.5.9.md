# Migration v1.5.8 → v1.5.9

v1.5.9 is a non-destructive staging-quality migration. No volumes, goals, memories, synapses, research history or staging drafts must be deleted.

1. Build/publish the v1.5.9 images.
2. Set `IMAGE_TAG=1.5.9`.
3. Keep `NEUROFORGE_OLLAMA_NUM_PREDICT=0` unless you deliberately want a global Ollama cap; otherwise the per-call 2,600-token staging budget can be silently constrained.
4. Keep the production Article-Depth defaults initially. Increase `NEUROFORGE_OLLAMA_NUM_CTX` before raising the Evidence-Prompt or article/token budgets substantially.
5. Recreate `neuroforge` and `neuroforge-worker` without deleting volumes.
6. Allow each active goal with a staging draft to complete one scheduler cycle. `staging_quality_gate_version` must become `staging-v4` before the draft satisfies an article target.
7. Review the new `article_quality` metadata. A production draft should normally have `text_chars >= 3500`, `answer_chars` in the configured summary range, and a successful claim-verification report when that gate is enabled.

Recommended upgrade command:

```bash
docker compose --profile research pull neuroforge neuroforge-worker
docker compose --profile research up -d --force-recreate neuroforge neuroforge-worker
```
