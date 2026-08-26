> **Historischer Stand:** Dieses Dokument beschreibt eine ältere Freigabelogik. Maßgeblich ist jetzt `HOTFIX-GLPI-KB-SIMPLE-AUTO-REPLY.md`. ITIL-Kategorien geben Artikel nicht mehr für Auto-Reply frei.

# Hotfix: Auto-Reply mit kategorielosen GLPI-KB-Artikeln

## Problem

GLPI kann einem Knowledge-Base-Artikel keine ITIL-/Ticketkategorie direkt zuweisen. Besitzt der Artikel außerdem keine GLPI-KB-Kategorie, liefert die Synchronisierung:

```text
glpi_kb_article_without_category
```

Die bisherige statische Freigabe konnte deshalb nicht erkennen, für welche Ticketkategorien der Artikel verwendet werden darf.

## Lösung

Neu:

```env
GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true
GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS=1
```

Ein kategorieloser Artikel wird damit nur bedingt freigegeben. Die tatsächliche Freigabe erfolgt beim Ticketlauf gegen:

```env
GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS=38,67
```

Die IDs werden in der `.env` konfiguriert; sie müssen und können nicht am GLPI-Artikel eingetragen werden.

## Sicherheitslogik

Ein Auto-Reply ist nur möglich, wenn:

1. `AUTO_REPLY=true`
2. `GLPI_KB_AUTO_REPLY=true`
3. `glpi-kb` in `KNOWLEDGE_ALLOWED_SOURCES` und `KNOWLEDGE_AUTO_REPLY_SOURCES` steht
4. `GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true`
5. die GLPI-KnowbaseItem-ID in `GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS` steht
6. die effektive Ticketkategorie in `GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS` steht
7. Retrieval, KI-Auswahl, Confidence, Evidenz, Sprache, Stil, Kontext und Antwortinhalt alle bestehen

Die ITIL-Allowlist wird nicht als Artikelkategorie gespeichert und erhöht nicht künstlich die Kategorie-Evidenz.

## Diagnose

Synchronisierung:

```text
glpi_kb_uncategorized_conditionally_approved
```

Passendes Ticket:

```text
Artikel ist für Auto-Reply freigegeben: ja
Erwartet: effektive Ticketkategorie in [38 67]
```

Nicht passende Ticketkategorie:

```text
reply_knowledge_auto_reply_category_not_allowed
```

## Beispiel

```env
AUTO_REPLY=true
KNOWLEDGE_ALLOWED_SOURCES=internal-kb,glpi-kb
KNOWLEDGE_AUTO_REPLY_SOURCES=internal-kb,glpi-kb
GLPI_KB_ENABLED=true
GLPI_KB_AUTO_REPLY=true
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=
GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS=4,5,6,7,8,9,10
GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true
GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS=1
```

Sehr breite ITIL-Allowlisten sollten zunächst im `DRY_RUN=true` getestet werden.
