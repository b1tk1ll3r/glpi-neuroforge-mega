# Control Center und Interaktion

Das Control Center ist absichtlich read-only. Es ist eine Beobachtungs- und Navigationsschicht, nicht der gemeinsame Super-Admin der Plattform.

## Warum read-only?

Ein einziges Dashboard mit GLPI-Schreibrechten, Knowledge-Editor-Rechten und NeuroForge-Admin-Token würde bei einem Fehler oder einer Kompromittierung alle Trust Boundaries gleichzeitig aufheben. Das Mega-Projekt trennt deshalb Statussicht und Schreibrechte.

## Wo Daten verändert werden

- **GLPI Agent:** policy-gated Ticket-/Followup-/Kategorie-/Eskalationsaktionen.
- **Knowledgebase:** Artikel bearbeiten, Staging prüfen und nach menschlicher Freigabe promoten.
- **NeuroForge Admin:** Brain-/Storage-/Provider-Verwaltung mit separatem Admin-Token.
- **Integration Draft API:** maschinelle Vorschläge ausschließlich nach Staging; `auto_reply=false` wird serverseitig erzwungen.

Das Control Center verlinkt diese Oberflächen und aggregiert Health/Readiness sowie die aktiven Vektor-Migrationsparameter. Es besitzt selbst keine Route, die Produktionsdaten verändert.

## Erweiterungsregel

Falls zentrale Aktionen später direkt im Control Center benötigt werden, sollten sie als einzelne delegierte Operationen mit eigenem Scope, Audit-Eintrag und expliziter Bestätigung implementiert werden. Die Admin-Credentials der Zielsysteme sollen nicht pauschal im Control Center hinterlegt werden.


## v1.2.0: Controlled-Autonomy-Status

Das Control Center zeigt zusätzlich die effektiven Stack-Schalter für:

- Controlled Learning
- Outcome Learning
- Research/SearXNG
- Autonomy

Diese Anzeigen sind bewusst nur Beobachtung. Das Aktivieren von Research oder Autonomy erfolgt über Betreiberkonfiguration/Compose bzw. NeuroForge-Admin, nicht über einen globalen Super-Admin-Schalter im Control Center.

## v1.3.0: Lernwirkung sichtbar machen

Das Control Center zeigt zusätzlich:

- Outcome Retrieval an/aus
- Retrieval-K
- Similarity-Floor
- fail-open/fail-closed der Erfahrungs-Suche
- Verfügbarkeit des read-only Quality-Replay-Endpunkts im Agenten

Die eigentlichen Laufzeitmetriken und Einzelfall-Evidenzen bleiben beim Agenten bzw. Prometheus. Das Control Center erhält dafür weiterhin keine Outcome-Schreib- oder NeuroForge-Adminrechte.

## v1.4 Unified Graph Explorer

The Control Center remains read-only. Its graph views use a dedicated Agent `CONTROL_READ_TOKEN` and the scoped NeuroForge app key. The Engineering Graph is embedded from a reproducible Go AST/Compose snapshot; optional Codebase Memory MCP is developer-only. See `UNIFIED-GRAPH.md`.
