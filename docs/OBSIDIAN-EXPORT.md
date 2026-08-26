# Obsidian / llm-wiki Export

Das Mega-Projekt kann die Wissensbasis als selbständigen Obsidian-Vault exportieren. Der Export verändert keine Quelldaten.

## Zwei Sichten

### Kanonische Knowledgebase

```text
GET /api/export/obsidian
```

Quelle sind die produktiven JSON-Dateien im gemeinsamen `knowledge/`-Verzeichnis. Der Export enthält Kategorien sowie explizite relation-artige Felder wie `linked_items`, `relations`, `related`, `related_articles`, `references`, `links`, `connections`, `associations` und `glpi_relations`.

### Live-Sicht des Agenten

```text
GET /api/knowledge/export/obsidian
```

Diese Sicht enthält zusätzlich die vom Agenten synchronisierten GLPI-KB-Artikel. Wenn die installierte GLPI-OpenAPI einen lesbaren `KnowbaseItem_Item`-Pfad bereitstellt, übernimmt der Sync die GLPI-Verknüpfungen (`knowbaseitems_id`, `itemtype`, `items_id`) in `linked_items`.

Ist die Relation-API nicht verfügbar oder fehlen Rechte, wird der KB-Artikel weiterhin synchronisiert. Der Agent protokolliert dann ausdrücklich, dass GLPI-Objektrelationen im Export fehlen.

## Vault-Struktur

```text
Wiki/
├── index.md
├── Schema.md
├── graph.json
├── .manifest.json
├── Knowledge/
├── Categories/        # kanonischer KB-Export
├── GLPI/              # Live-Agent-Export für GLPI-Objekte
└── Relations/         # generische Relation-Stubs
```

Artikel sind normales Markdown mit YAML-Frontmatter. Interne Beziehungen werden als Obsidian-Wikilinks `[[Wiki/...|Titel]]` geschrieben. Datumswerte sind ISO-8601-Daten (`YYYY-MM-DD`). `graph.json` enthält Knoten und Kanten zusätzlich maschinenlesbar.

## Export aus der Oberfläche

Sowohl Knowledgebase als auch Agent-Dashboard besitzen einen Button **„⇩ Obsidian Export“**.

## Export per Skript

```bash
# kanonische KB
KB_URL=http://127.0.0.1:8081 \
BASIC_AUTH_USER=admin \
BASIC_AUTH_PASSWORD='...' \
./scripts/export-obsidian.sh kb ./knowledge-vault.zip

# Live-Agent-Sicht inkl. GLPI-KB-Sync
AGENT_URL=http://127.0.0.1:8080 \
WEB_USERNAME=admin \
WEB_PASSWORD='...' \
./scripts/export-obsidian.sh agent ./live-vault.zip
```

Das Skript schreibt zunächst in eine temporäre Datei und ersetzt die Zieldatei erst nach einem erfolgreichen HTTP-Download.

## Governance

Der Export ist absichtlich read-only:

- keine Quelldatei wird geändert,
- keine GLPI-Verknüpfung wird zurückgeschrieben,
- keine Auto-Reply-Policy wird verändert,
- Secrets werden nicht in Frontmatter oder `graph.json` exportiert.

Damit kann der Vault in Obsidian, Git oder einem llm-wiki-artigen Workflow analysiert werden, ohne die operative Wissensbasis zu verändern.
