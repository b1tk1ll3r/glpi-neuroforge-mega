# GLPI NeuroForge Mega v1.5.9

v1.5.9 addresses a production-quality limitation observed after the v1.5.8 revalidation gate: source-grounded drafts could pass all evidence/claim checks while the generated knowledge article remained unnecessarily short. The root causes were a hard-coded 1,400-token synthesis budget, an even smaller syntax-repair budget, no minimum article-depth contract, and no explicit distinction between the full `text` article and the compact `answer` field.

## Changes

- The canonical KB article is explicitly the `text` field; `answer` remains a concise operational summary for downstream reply use.
- Production defaults: 3,500 minimum article characters, 6,500 target characters, 10,000 maximum characters, 160–1,200 answer characters.
- Staging synthesis receives a configurable 2,600-token output budget. Syntax repair and grounding rewrite use the same budget so repairs cannot silently truncate a valid long draft.
- Too-short/out-of-bounds drafts get exactly one evidence-only article-depth rewrite. No outside knowledge may be added; insufficient evidence remains fail-closed.
- Evidence text sent to the model is fairly budgeted across selected evidence items (14,000 evidence characters by default) to keep room for long output within common local-model context windows.
- Claim verification now inspects up to 32 material statements by default and uses a larger verification output allowance.
- `article_quality` audit metadata records text/answer length, configured bounds, evidence count, evidence prompt size, expansion status and synthesis output tokens.
- NeuroForge gets explicit `NEUROFORGE_OLLAMA_NUM_CTX` and `NEUROFORGE_OLLAMA_NUM_PREDICT` runtime overrides. The Mega default keeps `num_predict=0`, allowing call-specific budgets to take effect.
- Fortinet `support-forum` pages are no longer treated as authoritative primary documentation; Fortinet Technical-/Troubleshooting-Tip pages remain first-party authoritative.
- Quality-gate version advances to `staging-v4`, forcing one non-destructive revalidation of existing active drafts.

## Production defaults

```env
NEUROFORGE_OLLAMA_NUM_CTX=8192
NEUROFORGE_OLLAMA_NUM_PREDICT=0
NEUROFORGE_KB_STAGING_SYNTHESIS_MAX_TOKENS=2600
NEUROFORGE_KB_STAGING_EVIDENCE_PROMPT_MAX_CHARS=14000
NEUROFORGE_KB_STAGING_MIN_ARTICLE_CHARS=3500
NEUROFORGE_KB_STAGING_TARGET_ARTICLE_CHARS=6500
NEUROFORGE_KB_STAGING_MAX_ARTICLE_CHARS=10000
NEUROFORGE_KB_STAGING_MIN_ANSWER_CHARS=160
NEUROFORGE_KB_STAGING_MAX_ANSWER_CHARS=1200
NEUROFORGE_KB_STAGING_MAX_VERIFICATION_STATEMENTS=32
```

No storage reset is required. Existing goals, memories, synapses, research history and staging drafts remain intact.
