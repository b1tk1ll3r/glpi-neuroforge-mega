# NeuroForge v0.7.0

## Explainability
- Knowledge Explorer mit Memory-Typen, Status, Quellen, Graph, Detailansicht und Timeline.
- Persistente Knowledge Events für Lernen, Rewards, Feedback, Konsolidierung, Goals, Konflikte und Admin-Aktionen.
- Provenance pro neuem Memory: Quelle/Actor, Embedding- und Generation-Provider/Model/Node sowie Goal/Parent-Referenzen.
- Explainable Recall mit BaseScore, GraphBoost, TypeWeight, SalienceFactor, ConfidenceFactor und CandidateSource.

## Learning Policy
- getrennte Lernschalter für Chat-Input, Chat-Response, `/learn`, Imports und Goal-Cycles.
- Source-Trust → Confidence.
- Duplicate-Suppression.
- Mindestbestätigungen/-Confidence für semantische Konsolidierung.
- maximale Memory-Textlänge.
- optionales Archivieren stark negativ bewerteter Assistant-Antworten.
- Admin API + UI.

## Production hardening
- `/livez`, `/readyz`, `/version`.
- graceful shutdown und finaler Checkpoint.
- HTTP timeouts, Header-/Body-Limits und globales Concurrency-Limit.
- constant-time Tokenvergleiche.
- Security Header/CSP.
- Admin-Secrets standardmäßig maskiert; kein Admin-Token im Startlog.
- non-root/read-only Docker-Defaults.
- Prometheus Alert-Beispiele und Production Guide.

## Compatibility
- ältere Memories bleiben lesbar; fehlende Provenance wird nicht erfunden.
- bestehende Model-Routing-, WAL-, Segment-, HNSW- und Disk-PQ-Pfade bleiben erhalten.
