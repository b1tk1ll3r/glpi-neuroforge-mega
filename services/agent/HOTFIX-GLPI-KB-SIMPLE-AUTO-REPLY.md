# Hotfix: vereinfachte GLPI-KB-Auto-Reply-Freigabe

## Ziel

Die Grundfreigabe eines GLPI-Wissensartikels ist vollständig von der fachlichen Eignungsprüfung getrennt.

## Neue Freigaberegel

### Artikel mit GLPI-Knowledge-Base-Kategorie

Ein Artikel ist grundsätzlich für Auto-Reply freigegeben, wenn mindestens eine seiner GLPI-KB-Kategorie-IDs in dieser Liste steht:

```env
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=4,7
```

ITIL-/Ticketkategorien spielen für diese Grundfreigabe keine Rolle.

### Artikel ohne GLPI-Knowledge-Base-Kategorie

Ein unkategorisierter Artikel ist nur freigegeben, wenn beide Bedingungen erfüllt sind:

```env
GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true
GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS=1,5
```

Die IDs sind die numerischen GLPI-`KnowbaseItem`-IDs. `GLPI-KB-1` entspricht Artikel-ID `1`.

## Veraltete Variable

```env
GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS=
```

Die Variable wird aus Kompatibilitätsgründen noch eingelesen, aber nicht mehr ausgewertet. Ist sie befüllt, schreibt der Agent eine Warnung ins Log. Der Wert sollte geleert oder die Variable entfernt werden.

## Fachliche Eignung bleibt separat

Nach der Grundfreigabe müssen weiterhin alle fachlichen und technischen Gates bestehen:

- Retrieval-Floor,
- KI-Auswahl,
- KI-Confidence,
- finale Knowledge-Evidenz,
- Sprache und Kommunikationsstil,
- Antwortinhalt,
- Kontext- und Incident-Regeln,
- vorhandene Followups,
- Dry-Run-/Live-Schreibregeln.

Soweit GLPI ein Mapping von KB-Kategorien auf ITIL-Kategorien liefert, wird es nur für die separate Prüfung **„Artikel passt zur effektiven Ticketkategorie“** und für Kategorie-Evidenz verwendet. Fehlt das Mapping oder ist der Kategorie-Endpunkt nicht erreichbar, läuft der KB-Sync weiter; die Freigabe über die KB-Kategorie bleibt gültig.

## Neue Diagnoseentscheidungen

- `glpi_kb_auto_reply_approved`: Freigabe über eine GLPI-KB-Kategorie.
- `glpi_kb_uncategorized_article_approved`: Freigabe eines unkategorisierten Artikels über seine konkrete Artikel-ID.
- `glpi_kb_category_not_whitelisted`: Keine Artikel-KB-Kategorie steht in der Allowlist.
- `glpi_kb_article_without_category`: Artikel ist unkategorisiert, aber der Fallback ist deaktiviert.
- `glpi_kb_uncategorized_article_not_whitelisted`: Unkategorisierter Artikel ist nicht explizit freigegeben.
- `glpi_kb_auto_reply_whitelist_empty`: Für kategorisierte Artikel ist keine KB-Kategorie freigegeben.

Die Policyentscheidung bei fehlender Grundfreigabe lautet jetzt:

```text
reply_knowledge_auto_reply_not_approved
```

## Cache-Migration

Der GLPI-KB-Cache enthält eine Policy-Version. Caches aus der vorherigen ITIL-basierten Freigabelogik werden aus Sicherheitsgründen nicht geladen. Beim nächsten erfolgreichen GLPI-KB-Sync wird `data/glpi-kb-cache.json` automatisch im neuen Format erstellt.

## Empfohlene Konfiguration

```env
AUTO_REPLY=true
KNOWLEDGE_ALLOWED_SOURCES=internal-kb,glpi-kb
KNOWLEDGE_AUTO_REPLY_SOURCES=internal-kb,glpi-kb

GLPI_KB_ENABLED=true
GLPI_KB_AUTO_REPLY=true

# Kategorisierte Artikel
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=4,7

# Unkategorisierte Artikel
GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true
GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS=1,5

# Veraltet; leer lassen
GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS=
```
