# GLPI NeuroForge Mega v1.5.6

v1.5.6 hardens structured LLM output after a live BitLocker staging test exposed `invalid character \\ in string escape code` even after the syntax-repair pass. The failure was caused by local-model JSON strings containing literal Windows/registry/Markdown backslashes that were not valid JSON escapes.

## Structured-output hardening

- **Provider-native JSON mode:** Ollama structured calls for staging synthesis, syntax repair, claim verification and grounded rewrite now send `format: "json"`.
- **Strict schema decoding:** structured objects reject unexpected fields instead of silently accepting arbitrary model-added keys; arbitrary prose surrounding an object is no longer stripped/accepted.
- **Narrow deterministic escape repair:** when strict decoding fails, NeuroForge may preserve a literal backslash by JSON-escaping it only when it occurs inside a JSON string and is followed by a character that JSON does not define as an escape.
- **Valid escapes are untouched:** `\\`, `\"`, `\n`, `\t`, `\r`, `\/` and valid `\uXXXX` escapes retain their JSON semantics.
- **Fail-closed remainder:** malformed quoting, control characters, multiple JSON values, invalid code fences, unknown schema fields and other syntax defects are not broadly normalized. They either enter the single bounded syntax-repair pass or fail staging.
- **Shared verifier protection:** the same strict decoder/escape handling protects claim-verifier JSON and grounded rewrite JSON, not just the first synthesis response.

## Production-quality gates retained

All v1.5.5 source-authority, authoritative-action, claim-coverage, contradiction and human-review-only promotion gates remain enabled. This release changes structured transport/parsing robustness; it does not weaken evidence validation.

## Regression coverage

Tests cover Windows paths such as `C:\Windows\System32`, registry-like paths, a model-emitted backslash before a Markdown backtick, unknown schema fields, Markdown fences, missing outer braces, one bounded repair call and Ollama native JSON mode.
