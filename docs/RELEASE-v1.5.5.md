# GLPI NeuroForge Mega v1.5.5

v1.5.5 turns the autonomous Research → Staging path into a production-grounded pipeline. The release follows a live Windows 11 DISM `0x800f081f` test where the technical pipeline completed but the synthesized draft recommended the wrong SFC/DISM order and relied mostly on non-primary web sources.

## Production quality gates

- **Source authority ranking:** selected evidence is ordered by first-party/vendor authority before confidence/recency while retaining source diversity.
- **Authoritative-source requirement:** production Compose requires at least one authoritative source in the evidence actually supplied to the synthesizer. Operator-specific domains can be added without replacing the built-in vendor set.
- **Vendor-community distinction:** Microsoft Q&A under `learn.microsoft.com/.../answers/...` remains useful corroboration but is not counted as primary Microsoft documentation.
- **Authority-aware query planning:** deterministic goal research reserves a first-party `site:` query for recognized vendors such as Microsoft, Fortinet, NVIDIA, Cisco, Broadcom/VMware, Red Hat, Ubuntu and Apple.
- **Critical identifier guard:** newly synthesized error codes, CVEs, Microsoft KB IDs, versions and command switches must already exist in the selected evidence bundle.
- **Claim → Evidence verification:** a separate critic pass receives numbered draft statements (`S*`) and numbered evidence (`E*`) and must account for every material statement with explicit evidence IDs.
- **Authoritative action gate:** commands/recommendations require at least one authoritative evidence item when the production default is enabled.
- **Contradiction handling:** unsupported or contradicted statements fail closed. One evidence-only grounding rewrite may remove/correct unsupported content; the entire rewritten draft is then verified again.
- **Auditable JSON:** Staging metadata now persists source authority, independent origins, authoritative-source count, claim coverage, statement evidence IDs, contradictions and repair status.
- **Independent corroboration semantics:** multiple URLs/pages on the same registrable source origin no longer inflate `research_corroborations` as independent confirmation.

## Defaults

The root Compose enables the production gates by default:

```env
NEUROFORGE_KB_STAGING_REQUIRE_AUTHORITATIVE_SOURCE=true
NEUROFORGE_KB_STAGING_MIN_AUTHORITATIVE_SOURCES=1
NEUROFORGE_KB_STAGING_VERIFY_CLAIMS=true
NEUROFORGE_KB_STAGING_MIN_CLAIM_COVERAGE=1.0
NEUROFORGE_KB_STAGING_REQUIRE_AUTHORITATIVE_ACTIONS=true
NEUROFORGE_KB_STAGING_MAX_VERIFICATION_STATEMENTS=24
NEUROFORGE_KB_STAGING_VERIFICATION_REPAIR=true
```

`NEUROFORGE_KB_STAGING_AUTHORITATIVE_DOMAINS` is additive. Keep it empty unless the deployment uses first-party documentation hosts not covered by the built-ins.

## Governance

The bridge still writes **only** human-review staging. The Knowledge integration enforces `auto_reply=false`; production Knowledge promotion remains a human action.
