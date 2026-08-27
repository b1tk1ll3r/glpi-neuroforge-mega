# GLPI NeuroForge Mega v1.5.4

v1.5.4 hardens the LLM staging-synthesis boundary after a live Windows 11 DISM 0x800f081f test showed that a local Ollama model could return JSON wrapped in Markdown or malformed structured output, causing `invalid staging synthesis JSON: invalid character '`' looking for beginning of value`.

## Changes

- The staging prompt explicitly requires a JSON object whose first character is `{` and last character is `}` and forbids Markdown/code fences.
- A single outer ` ```json ` fence is safely removed before decoding.
- The narrow local-model defect of emitting JSON members without the outer object braces is normalized only when the output is recognizably JSON-like and contains the required `answer` member.
- Any other syntax failure triggers exactly one syntax-only LLM repair pass. That repair is forbidden from adding, inferring, or correcting facts.
- The repaired draft still has to pass the existing minimum-content and goal-relevance gates. If repair or validation fails, staging remains fail-closed and no article is published.
- Regression tests cover fenced JSON, missing outer braces, and malformed structured output repaired on the second model call.
