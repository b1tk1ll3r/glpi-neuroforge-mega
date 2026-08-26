# Migration v0.7.3 → v0.8.0

Die Migration ist rückwärtskompatibel. Vorher vollständiges Backup des Datenverzeichnisses erstellen.

## Neue Daten

- `state.json` enthält nun Knowledge-Source-Metadaten und Goal-Schedule/Research-Felder.
- Bei `ingestion.store_original=true` entsteht `data/sources/`; dieses Verzeichnis in Backups aufnehmen.
- Bestehende Memories bleiben gültig. Legacy-Provenance wird nicht erfunden.

## Neue Defaults

- Research/SearXNG bleibt nach Upgrade deaktiviert und muss bewusst im Admin-Reiter **Research** aktiviert werden.
- Goal Research kann pro Ziel aktiviert werden.
- Neue Goals können über `autonomy.run_on_goal_create` sofort fällig werden; vorhandene aktive Goals werden mit einem sinnvollen Intervall migriert.
- Standard-HTTP-Body-Limit ist für neue/alte Default-Installationen auf 32 MiB angehoben, damit Dokumentupload bis zum konfigurierten Ingestion-Limit möglich ist.

## SearXNG

SearXNG muss JSON-Ausgabe erlauben. Beispiel: `deploy/searxng/settings.yml.example`. Danach im Dashboard Base-URL setzen, Verbindung testen und erst anschließend `Research enabled` aktivieren.

## PDF

Native Binary: `pdftotext`/Poppler installieren, wenn PDFs verarbeitet werden sollen. Das v0.8 Server-Containerimage enthält `poppler-utils`.
