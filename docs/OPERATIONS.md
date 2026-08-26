# Betrieb

## Standardbefehle

```bash
make test
make vet
make build
make up
# optional: Research/SearXNG ohne Autonomy
make research-up
make ps
make status
make logs
make down
```

## Secrets

```bash
./scripts/generate-secrets.sh
```

Die Ausgabe wird nicht automatisch in `.env` geschrieben. Das verhindert, dass vorhandene Credentials versehentlich überschrieben werden.

## Status

Das Control Center ist read-only und fragt parallel ab:

- Agent `/readyz`
- Knowledgebase `/api/health`
- NeuroForge `/api/v1/stats` mit App Key

Es zeigt keine Secrets und besitzt keine Schreibroute.

## Knowledge Sync

`knowledge/` ist das gemeinsame kanonische Verzeichnis:

- Knowledgebase: read/write
- Agent: read-only

Der Agent erkennt Änderungen inkrementell. In `dual`/`neuroforge` werden veränderte Chunk-Vektoren in das NeuroForge-Namespace synchronisiert. Entfernte Dokumente werden dort ebenfalls entfernt.

## Externe Knowledge-Connectoren

Connector-Dokumente werden ebenfalls nach NeuroForge gespiegelt. Ihr lokaler Connector-Vektorcache wird derzeit bewusst beibehalten, damit Connector-Neustarts und Fail-open-Betrieb nicht bei jedem Zyklus neu einbetten müssen. Das ist eine Resilienz-/Effizienzentscheidung und unterscheidet sich von der Externalisierung der lokalen produktiven KB.


## Obsidian-Export

```bash
./scripts/export-obsidian.sh kb ./knowledge-vault.zip
./scripts/export-obsidian.sh agent ./live-vault.zip
```

Der KB-Export liest die kanonischen JSON-Dateien. Der Agent-Export ergänzt synchronisierte GLPI-KB-Artikel und verfügbare `KnowbaseItem_Item`-Verknüpfungen. Beide Exporte sind read-only. Details: [`OBSIDIAN-EXPORT.md`](OBSIDIAN-EXPORT.md).

## Research-Drafts

Ein Proposal-JSON kann kontrolliert ins Staging geschrieben werden:

```json
{
  "source": "NeuroForge Research",
  "query": "VPN Fehlerbild",
  "title": "VPN Diagnose",
  "text": "Beobachtetes Symptom ...",
  "answer": "1. ...",
  "categories": ["VPN"],
  "keywords": ["gateway", "token"],
  "min_score": 0.85
}
```

```bash
export KB_INTEGRATION_TOKEN='...'
./scripts/propose-draft.sh proposal.json
```

Der Server erzwingt `auto_reply=false`. Promotion erfolgt im normalen Editor.

## SQAR

SQAR ist ausschließlich im NeuroForge Vector Journal aktiviert. Nicht komprimiert werden operative Audit-/Policy-Dateien oder zufällig zugreifbare Memory-Segmente. Der Codec wählt nur dann die SQAR-Variante, wenn sie gegenüber der Roh-/DEFLATE-Darstellung tatsächlich kleiner ist.


## Controlled Learning / Human Outcomes

Im Standard ist `NEUROFORGE_CONTROLLED_LEARNING=true`. Rohe Chat-/Assistant-Inhalte werden damit nicht automatisch als Langzeitwissen gelernt. Ein Agent-Run kann im Dashboard explizit bestätigt oder korrigiert werden. Das Outcome wird unter `DATA_DIR/ticket-outcomes.json` auditiert und erst dann über den App-Key-Pfad an NeuroForge übertragen.

Bei `OUTCOME_LEARNING_FAIL_OPEN=false` ist ein NeuroForge-Syncfehler für den Techniker sichtbar. Der lokale Outcome-Eintrag bleibt erhalten und kann durch Wiederholen derselben Entscheidung retryt werden. Änderungen am GLPI-Ticket seit dem analysierten Run blockieren die Validierung.

## Optionales SearXNG / Research

Der Basisstack startet SearXNG nicht. Für Research zuerst einen echten `SEARXNG_SECRET` in `.env` setzen und dann:

```bash
./scripts/research-up.sh
```

Das startet das Compose-Profil `research` und schaltet Research/SearXNG für NeuroForge ein. `NEUROFORGE_AUTONOMY_ENABLED` bleibt separat und standardmäßig `false`. Details: [`CONTROLLED-AUTONOMY.md`](CONTROLLED-AUTONOMY.md).
