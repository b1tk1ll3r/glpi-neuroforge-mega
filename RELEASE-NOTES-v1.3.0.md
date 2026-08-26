# Release Notes v1.3.0 — Closed Learning Loop

## Schwerpunkt

v1.3.0 schließt die wichtigste Produktionslücke aus v1.2.0: menschlich validierte Helpdesk-Erfahrung wird nicht nur gespeichert, sondern bei späteren ähnlichen Tickets wieder als kontrollierte Evidenz genutzt. Gleichzeitig bleiben offizielle Knowledge-Artikel die einzige Autorität für Auto-Reply.

## Neu

- App-Key-geschützte Outcome-Suche `POST /api/v1/integrations/outcomes/search`
- Retrieval ausschließlich aus aktiven `glpi.outcome.accepted|corrected`-Memories
- menschlich validierte Outcomes als sekundäre Evidenz im Reply-Kontext
- Outcome-Evidenz kann niemals selbst eine Knowledge-ID autorisieren
- expliziter LLM-Prompt-Guard gegen das Einführen nicht durch die KB belegter Lösungen
- echte NeuroForge-Supersession: eine Korrektur setzt die frühere Outcome-Memory auf `superseded`
- Revisionskante `new.Supersedes -> oldID` bleibt auditierbar
- supersedete Outcomes werden nicht mehr gesucht
- korrigierte aktive Outcome-Memories enthalten die alte falsche KI-Antwort nicht mehr im semantisch durchsuchbaren Text
- in-memory Provenance-Source-Index für source-/namespace-begrenzte Fallback-Suchen
- Agent-KPIs für Outcome-Suchen, Treffer, Fehler, Accepted/Corrected/Failed/Idempotent
- NeuroForge-KPIs für NFVJ2/SQAR: raw/stored bytes, Savings, SQAR-/Compressed-Blocks
- read-only Quality-Replay API `POST /api/quality/replay`
- `scripts/quality-replay.py` + Beispiel-Dataset
- Replay-Kennzahlen: Knowledge Recall@K, Knowledge MRR, Outcome Recall@K, Outcome MRR, Experience-Rescue-Cases
- Agent-WebUI zeigt validierte Outcome-Kandidaten und Suchdauer/-fehler pro Run
- Agent-Konfiguration und Control Center zeigen Outcome-Retrieval-K, Similarity-Floor und Failure Policy

## Sicherheitsmodell

Der Agent führt weiterhin die verbindlichen GLPI-Policies aus. Ein validiertes Outcome ist Erfahrungswissen, kein freigegebener Knowledge-Artikel. Deshalb gilt weiterhin:

```text
validated outcome alone != auto reply authority
```

Für einen Auto-Reply muss weiterhin ein freigegebener Knowledge-Kandidat die bestehenden Retrieval-, Source-, Category-, Evidence- und Confidence-Gates bestehen.

## Skalierung

Der NeuroForge-Fallback für Provenance-/Namespace-Suchen iteriert nicht mehr über den kompletten Memory-Katalog. Ein rebuildbarer In-Memory-Index `provenance source -> memory IDs` begrenzt den Exact-Fallback auf die jeweilige Source. Der globale ANN-Index bleibt für die schnelle Kandidatengewinnung bestehen.

## Qualitätsmessung

Der neue Replay-Endpunkt ist read-only und verändert weder GLPI noch Knowledge noch NeuroForge. Er ist für einen historischen Ticket-Korpus gedacht, damit nicht nur technische Persistenz, sondern die tatsächliche Retrieval-Wirkung des Lernens gemessen werden kann.

## Upgrade

Siehe `docs/MIGRATION-v1.2.0-to-v1.3.0.md`, `docs/QUALITY-REPLAY.md` und `docs/CONTROLLED-AUTONOMY.md`.
