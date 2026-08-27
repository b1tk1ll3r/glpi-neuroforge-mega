# GLPI NeuroForge Mega v1.5.8

v1.5.8 closes a production-state consistency bug found while revalidating existing FortiClient, DISM and BitLocker goals after the v1.5.7 identifier-grounding fix. Older `last_staging_error` values could remain visible forever because a goal with an existing draft returned early whenever the newest research run contributed no new evidence. The old draft also continued to satisfy an article target even if it had never passed the current quality gate.

## Staging revalidation

- Goals persist `staging_draft_validated`, `staging_quality_gate_version` and a hashed `last_staging_attempt_signature`.
- Existing drafts from an older gate are revalidated once against the current gate using already persisted relevant evidence.
- Unchanged, already validated drafts are not regenerated on empty scheduler cycles.
- Unchanged deterministic failures are not retried every cycle; the evidence signature prevents expensive LLM retry loops.
- Transient transport/publisher failures remain retryable.
- If current evidence no longer satisfies the quality threshold, the old draft is marked not validated and the stale error is replaced by the current quality state.

## Goal progress semantics

For article/draft targets, historical `staging_drafts_created` is audit history only. Progress is now based on whether the current stable staging draft exists and is validated under the current quality-gate version. This prevents `1/1 = 100%` for legacy or invalidated drafts.

## Compatibility

No destructive data migration is required. New fields are optional JSON fields and are populated by the next goal cycle. Existing goals, memories, synapses, research runs and staging drafts are retained.
