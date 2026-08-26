> **Historischer Stand:** Dieses Dokument beschreibt eine ältere Freigabelogik. Maßgeblich ist jetzt `HOTFIX-GLPI-KB-SIMPLE-AUTO-REPLY.md`. ITIL-Kategorien geben Artikel nicht mehr für Auto-Reply frei.

# Hotfix: GLPI-KB-Artikel für Auto-Antworten freigeben

## Problem

Ein synchronisierter GLPI-Wissensartikel konnte die Prüfung zur effektiven Ticketkategorie bestehen und trotzdem an folgender Regel scheitern:

```text
Artikel ist für Auto-Reply freigegeben
```

Das sind zwei getrennte Prüfungen:

1. **Kategorie-Scope:** Passt die gemappte GLPI-Ticket-/ITIL-Kategorie des Artikels zum Ticket?
2. **Artikel-Freigabe:** Wurde der synchronisierte GLPI-KB-Artikel ausdrücklich für automatische Antworten freigegeben?

Bisher konnte die zweite Freigabe ausschließlich über die separaten **GLPI-Knowledge-Base-Kategorie-IDs** erfolgen:

```env
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=...
```

Wer dort versehentlich die im Ticket sichtbaren ITIL-Kategorie-IDs eingetragen hat, erhielt einen positiven Kategorie-Scope, aber weiterhin `auto_reply=false`.

## Neue Konfigurationsmöglichkeit

Zusätzlich steht jetzt eine Whitelist für die gemappten Ticket-/ITIL-Kategorien zur Verfügung:

```env
GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS=38,67
```

Ein GLPI-KB-Artikel wird grundsätzlich für Auto-Reply markiert, wenn alle allgemeinen Voraussetzungen gelten und mindestens eine der beiden ausdrücklich konfigurierten Whitelists trifft:

```env
GLPI_KB_AUTO_REPLY=true
KNOWLEDGE_ALLOWED_SOURCES=internal-kb,glpi-kb
KNOWLEDGE_AUTO_REPLY_SOURCES=internal-kb,glpi-kb

# Variante A: separate GLPI-KB-Kategorie-IDs
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=4,7

# Variante B: im Ticket sichtbare ITIL-Kategorie-IDs
GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS=38,67
```

Sind beide Listen befüllt, genügt ein Treffer in einer der Listen. Ohne mindestens eine Liste verweigert die Konfigurationsprüfung den Start bei `GLPI_KB_AUTO_REPLY=true`.

## Diagnose

Jeder synchronisierte GLPI-KB-Artikel enthält nun:

```json
{
  "auto_reply": false,
  "auto_reply_decision": "glpi_kb_category_not_whitelisted",
  "auto_reply_detail": "GLPI-KB-Kategorien: [9]; gemappte ITIL-Kategorien: [38]; freigegebene GLPI-KB-Kategorien: [4, 7]; freigegebene ITIL-Kategorien: []; keine konfigurierte Freigabe-Whitelist trifft zu"
}
```

Mögliche Entscheidungen sind unter anderem:

| Entscheidung | Bedeutung |
|---|---|
| `glpi_kb_auto_reply_approved` | Eine konfigurierte KB- oder ITIL-Whitelist trifft zu. |
| `glpi_kb_auto_reply_disabled` | `GLPI_KB_AUTO_REPLY=false`. |
| `glpi_kb_article_without_category` | Der Artikel besitzt keine aus GLPI gelesene KB-Kategorie. |
| `glpi_kb_category_not_mapped_to_itil` | Die KB-Kategorie ist keiner Ticket-/ITIL-Kategorie zugeordnet. |
| `glpi_kb_category_not_whitelisted` | Weder die KB- noch die ITIL-Whitelist trifft zu. |
| `glpi_kb_auto_reply_whitelist_empty` | Keine Whitelist ist wirksam. |

Die Regel **„Artikel ist für Auto-Reply freigegeben“** zeigt diese Ursache jetzt direkt im Detailtext. Auch der Knowledge-Inspector und die Kandidatenaudits enthalten die Freigabeentscheidung.

Das Dashboard zeigt außerdem:

- Anzahl freigegebener GLPI-KB-Artikel,
- Anzahl blockierter GLPI-KB-Artikel,
- Verteilung der Freigabeentscheidungen,
- konfigurierte KB-Kategorie-IDs,
- konfigurierte ITIL-Kategorie-IDs.

## Migrationsbeispiel

Wenn bisher beispielsweise die Ticketkategorie `38` irrtümlich hier eingetragen war:

```env
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=38
```

sollte die Konfiguration geändert werden zu:

```env
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=
GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS=38
```

Der Agent gibt zusätzlich eine Warnung aus, wenn Werte in `GLPI_KB_AUTO_REPLY_CATEGORY_IDS` nicht als KB-Kategorie vorkommen, aber als ITIL-Kategorie existieren.

## Nach der Änderung

1. Agent neu starten.
2. Auf den Logeintrag `GLPI knowledge base synchronized` warten.
3. Dort `auto_reply_approved` und `auto_reply_blocked` prüfen.
4. Im Knowledge-Inspector den Artikel öffnen.
5. Einen neuen Ticketlauf oder eine manuelle Neuanalyse starten.

Der bestehende `glpi-kb-cache.json` wird beim erfolgreichen initialen Sync überschrieben. Historische Ticketläufe werden nicht rückwirkend verändert.
