# GLPI NeuroForge Mega v1.5.7

v1.5.7 fixes a production-grounding false positive observed during live BitLocker, DISM and FortiClient goal revalidation. The v1.5.6 critical-identifier scanner treated every slash-prefixed word as a possible CLI switch, so normal German compounds and URL-path fragments such as `BIOS-/UEFI`, `/portal-konfiguration` or `/interaktionsbereiche` could block an otherwise grounded staging draft.

## Identifier-grounding hardening

- Slash-prefixed identifiers are no longer extracted globally from prose.
- CLI-style slash switches are extracted only from Markdown code spans/fences, where the author/model explicitly marks the content as code.
- URL paths, filesystem-like fragments and hyphen/slash compounds are not treated as command switches.
- Real code-marked switches remain fail-closed: an invented option such as `DISM /MagicRepair` is rejected unless it occurs in the evidence bundle.
- Sourced switches such as `/Online`, `/Cleanup-Image` and `/RestoreHealth` continue to pass.
- Existing global checks for error codes, CVEs, KB identifiers and version numbers remain unchanged.
- The v1.5.5 source-authority/claim-verification gates and the v1.5.6 strict structured-JSON path remain fully active.

## Regression coverage

Tests reproduce `BIOS-/UEFI`, `Web-/Portal-Konfiguration`, URL-path text, an invented code-marked `/MagicRepair` switch and a sourced `/RestoreHealth` command. Test, vet, build and race gates are required before packaging.
