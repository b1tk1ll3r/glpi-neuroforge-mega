# GLPI NeuroForge Mega v1.5.1

v1.5.1 fixes a research-quality failure discovered during the first real autonomous staging run: an NVIDIA goal could ingest an unrelated MDN WebRTC page and publish raw page chunks as a supposed knowledge article.

The release adds a fail-closed relevance gate, durable goal provenance, relevant-evidence reconciliation, source-diverse draft selection, mandatory production LLM article synthesis, output relevance validation, correct article-target progress semantics, external Ollama Compose support, and the remaining worker-token Compose cleanup.

Local release gates: all four Go modules pass `go test ./...`, `go vet ./...`, `go build ./...`, and the targeted race suites from `scripts/release-gate.sh`.
