# Release Notes v1.2.0 — Controlled Autonomy

## Schwerpunkt

v1.2.0 macht aus „autonom lernfähig“ ein kontrolliert autonomes Betriebsmodell. Rohes Chat-/Modellverhalten wird im Mega-Stack nicht mehr automatisch zu vertrauenswürdigem Langzeitwissen. Helpdesk-Lernen folgt stattdessen dem Ablauf **Ticket → KI-Vorschlag → Techniker bestätigt/korrigiert → Outcome → Learn**.

## Neu

- optionaler SearXNG-Service als Compose-Profil `research`
- gehärtete private SearXNG-Konfiguration unter `deploy/searxng/settings.yml`
- `scripts/research-up.sh` für bewusstes Research-Enabling
- separate Schalter für Research/SearXNG und zeitgesteuerte Autonomy
- `NEUROFORGE_CONTROLLED_LEARNING=true` als konservativer Mega-Stack-Standard
- kein automatisches Lernen von Chat-Inputs oder Assistant-Antworten im Controlled Mode
- neue App-Key-geschützte API `POST /api/v1/integrations/outcomes`
- serverseitig gesetzte Provenance `glpi.outcome.accepted|corrected`
- Agent-Audit `ticket-outcomes.json` mit `pending|learned|failed`
- Stale-Run-Schutz: Trusted Outcome nur, wenn der GLPI-Ticketzustand noch zum analysierten Run passt
- unveränderliche Outcome-Revisionskette via `supersedes_id`
- idempotente Wiederholung bereits gelernter menschlicher Entscheidungen
- UI-Aktionen **KI-Antwort bestätigen** und **KI-Antwort korrigieren** im Run-Drawer
- Control Center zeigt Controlled Learning, Outcome Learning, Research/SearXNG und Autonomy read-only an
- `SEARXNG_SECRET` im Secret-Generator

## Vertrauensmodell

- Web Search: 0.45
- Web Page/Document: 0.60
- human accepted outcome: 1.00
- human corrected outcome: 1.00

Web-Evidence bleibt source-backed, deduplizierbar und korroborierbar. Sie wird nicht mit einem menschlich bestätigten Helpdesk-Outcome gleichgesetzt.

## Bewusste Grenzen

- SearXNG ist standardmäßig aus.
- Research ist standardmäßig aus.
- Autonomy ist standardmäßig aus.
- Research darf nicht direkt in die Produktions-KB schreiben.
- Das Control Center bleibt read-only.
- GLPI-Aktionen bleiben beim policy-gated Agenten.

## Upgrade

Siehe `docs/MIGRATION-v1.1.0-to-v1.2.0.md` und `docs/CONTROLLED-AUTONOMY.md`.
