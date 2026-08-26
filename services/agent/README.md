# GLPI AI Agent (Go + Ollama)

Produktionsorientierter, bewusst **policy-gesteuerter** Ticket-Agent für GLPI 11. Er liest neue/geänderte Tickets über die GLPI High-Level API, führt getrennte KI-Läufe für Kategorie, Priorität, Störungszuordnung und Antwortauswahl aus und kann offene Tickets in einem unabhängigen Scheduler auf Eskalationsbedarf prüfen. Schreiboperationen erfolgen ausschließlich nach deterministischen Go-Policies.

## Sicherheitsmodell

- `DRY_RUN=true` ist der Default.
- `AUTO_REPLY=false` ist der Default.
- Das LLM erhält **keine GLPI-Tools** und kann keine Schreiboperation direkt auslösen.
- Kategorie-IDs werden gegen die aus GLPI geladene Kategorie-Liste validiert.
- Automatische Antworten stammen **nicht aus freiem LLM-Text**, sondern aus einem freigegebenen Knowledge-Dokument (`auto_reply=true`).
- Der Knowledge-Index ist fail-closed: Geladen wird nur die Vereinigung aus `KNOWLEDGE_ALLOWED_SOURCES` und `KNOWLEDGE_CATEGORY_SOURCES`.
- Einträge aus `KNOWLEDGE_CATEGORY_SOURCES` werden ausschließlich für die Kategorieentscheidung verwendet und sind keine Antwortkandidaten.
- Auto-Replies benötigen zusätzlich eine Quelle aus `KNOWLEDGE_AUTO_REPLY_SOURCES` sowie passende Sprach-/Stil-Metadaten.
- Endnutzer-Antworten werden zentral mit konfigurierter Anrede, Grußformel und Signatur gerahmt.
- Vor einer Antwort werden Followups zweimal geprüft: vor der KI-Analyse und unmittelbar vor dem Schreiben.
- Sobald irgendein Followup existiert, antwortet der Agent nicht.
- Pro Ticket wird innerhalb eines Prozesses seriell gearbeitet; Polling/Webhook-Ereignisse werden dedupliziert.
- GLPI-Schreibfehler werden nicht automatisch wiederholt, um Doppelwrites zu vermeiden.
- Das Dashboard ist read-only und standardmäßig mit HTTP Basic Auth geschützt.
- Jeder KI-Schritt wird als eigenständiger `AnalysisRun` mit Input-Hash, Prompt-Version, Reason Codes, Policy-Checks und Action-Audit gespeichert.
- Zeitgesteuerte Eskalationsläufe sind von `date_mod` und der normalen Ticket-Deduplizierung unabhängig.
- Die interne Verarbeitung nutzt eine deduplizierende Prioritätsqueue; Webhooks, manuelle Läufe, Polling und Scheduler besitzen getrennte Prioritäten.
- Audit-Trail: `data/runs.jsonl` mit automatischer Kompaktierung bei starkem Wachstum. Der kompakte Betriebszustand für letzte Ticketversionen und ausgeführte Eskalationsstufen liegt getrennt in `data/state-index.json`.

> Wichtige Grenze: Die zweite Followup-Prüfung minimiert Race Conditions, kann ohne einen atomaren Conditional-Write auf GLPI-Seite aber kein mathematisch vollständig atomisches "check-and-write" garantieren. Für einen einzelnen Agent-Prozess ist zusätzlich ein Ticket-Lock aktiv.

## Voraussetzungen

- GLPI 11.0.6+ empfohlen (API v2.3).
- High-Level API in GLPI aktiviert.
- OAuth Client in **Setup > OAuth Clients** mit Password Grant und `api` Scope.
- Dedizierter GLPI-Servicebenutzer mit minimal nötigen Rechten: Tickets lesen, Kategorien lesen/ändern (falls genutzt), Followups lesen/hinzufügen (falls Auto-Reply genutzt).
- Ollama mit Chat- und Embedding-Modell.

Beim Start lädt der Agent `/api.php/doc.json` und prüft, ob die erwarteten Kernrouten vorhanden sind. Dadurch schlägt ein API-Mismatch früh und sichtbar fehl. Die mitgelieferten Tests laufen gegen HTTP-Mocks; eine echte GLPI-Instanz konnte in dieser Build-Umgebung nicht angebunden werden, daher ist der Shadow-Mode auf deiner Installation vor Live-Schreibzugriff zwingend.

## Start nativ unter Windows / PowerShell

Für einen nativen Windows-Start **nicht** die Docker-Pfade `/app/data`, `/app/knowledge` oder den Docker-Hostnamen `ollama` verwenden. Die mitgelieferte `.env.example` enthält deshalb jetzt native, plattformneutrale Defaults:

```env
DATA_DIR=./data
KNOWLEDGE_DIR=./knowledge
OLLAMA_URL=http://localhost:11434
```

Einmalig:

```powershell
Copy-Item .env.example .env
# Danach .env mit den echten GLPI-Zugangsdaten bearbeiten.
ollama pull qwen3:8b
ollama pull embeddinggemma
```

Start:

```powershell
.\run.ps1
```

`run.ps1` lädt `.env`, startet immer aus dem Projektverzeichnis und erkennt zur Migration auch alte Docker-Werte. Beispielsweise wird ein vorhandenes `KNOWLEDGE_DIR=/app/knowledge` beim nativen Windows-Start auf `<Projekt>\knowledge` umgesetzt und mit einer Warnung ausgegeben. Das Datenverzeichnis wird bei Bedarf erstellt; ein fehlendes Knowledge-Verzeichnis führt zu einer verständlichen Fehlermeldung statt zu einem Panic.

Docker Compose überschreibt diese drei nativen Werte im Container weiterhin explizit mit `/app/data`, `/app/knowledge` und `http://ollama:11434`.

## Start mit Docker Compose

```bash
cp .env.example .env
$EDITOR .env

docker compose up -d ollama
docker compose exec ollama ollama pull qwen3:8b
docker compose exec ollama ollama pull embeddinggemma

docker compose up -d --build agent
```

Dashboard: `http://127.0.0.1:8080/`

Vor dem ersten Live-Betrieb unbedingt mehrere Tage/Wochen im Shadow Mode lassen:

```env
DRY_RUN=true
AUTO_CATEGORY=true
AUTO_REPLY=false
PRIORITY_ENABLED=true
AUTO_PRIORITY=false
ESCALATION_ENABLED=false
AUTO_ESCALATION=false
```

Danach zunächst nur Kategorieänderungen:

```env
DRY_RUN=false
AUTO_CATEGORY=true
AUTO_REPLY=false
```

Erst nach Auswertung der Audit-Daten einzelne KB-Einträge mit `auto_reply=true` freigeben und anschließend:

```env
AUTO_REPLY=true
GLPI_AGENT_USER_ID=123
```

## Getrennte KI-Läufe: Priorität und Eskalation

Jede Analyse besitzt eine eigene ID und wird unabhängig diagnostiziert. Der übergeordnete Ticketlauf enthält lediglich die zeitliche und kausale Klammer. Die Diagnose zeigt je Analyse unter anderem Modell, Prompt-Version, Eingabe-Snapshot und -Hash, strukturierte Entscheidung, Grundcodes, Confidence, Policy-Gates und die tatsächlich ausgeführte Aktion.

Die Prioritätsanalyse ist standardmäßig aktiv, aber im Shadow Mode:

```env
PRIORITY_ENABLED=true
AUTO_PRIORITY=false
PRIORITY_CONFIDENCE=0.88
PRIORITY_MAX_INCREASE=1
```

Das Modell empfiehlt eine GLPI-Priorität und kontrollierte Grundcodes. Die Go-Policy verhindert Herabstufungen, begrenzt Erhöhungen und akzeptiert nur konfigurierte Gründe. Für einen kontrollierten Live-Betrieb sind **beide** Schalter erforderlich:

```env
DRY_RUN=false
AUTO_PRIORITY=true
```

Die Eskalation verwendet einen separaten Scheduler und findet deshalb auch unveränderte, ältere Tickets:

```env
ESCALATION_ENABLED=true
AUTO_ESCALATION=false
ESCALATION_SCAN_INTERVAL=15m
ESCALATION_MIN_AGE=4h
GLPI_ESCALATION_FILTER=status.id==1
```

Zuerst sollte `AUTO_ESCALATION=false` bleiben. Der Scheduler erzeugt dann vollständige Eskalationsanalysen, führt aber keine Aktion aus. Implementiert sind `raise_priority`, `assign_second_level`, `assign_security_team`, `notify_service_owner`, `link_major_incident` und `request_manager_review`. Das Modell kann höchstens drei Aktionen empfehlen; jede wird separat gegen Zielkonfiguration, Grundcodes, Mindeststufe und Idempotenz geprüft und als eigener Action-Audit-Schritt gespeichert. Erfolgreiche Schritte werden mit Ticket, Stufe, Aktion und Ziel in `state-index.json` dedupliziert. Followups des konfigurierten Agent-Benutzers werden bei der Inaktivitätsberechnung ausgenommen. Für `AUTO_ESCALATION=true` muss `GLPI_AGENT_USER_ID` auf das dedizierte GLPI-Agentkonto zeigen; andernfalls verweigert die Konfiguration den Start. Die vollständige Konfiguration und Einführungsreihenfolge steht in [ESCALATION.md](ESCALATION.md).

## GLPI-Endpunkte

Default ist `GLPI_API_VERSION=v2.3`. Der Client verwendet:

- OAuth: `POST /api.php/token`
- Tickets lesen und Eskalationskandidaten suchen: `/api.php/v2.3/Assistance/Ticket`
- Kategorie, Priorität und konfigurierte Bearbeiter-/Gruppenzuweisungen schreiben: `PATCH /api.php/v2.3/Assistance/Ticket/{id}`
- Öffentliche und private Followups: `/api.php/v2.3/Assistance/Ticket/{id}/Timeline/Followup`
- Major-Incident-Verknüpfung: installationsspezifischer, ausdrücklich über `GLPI_ESCALATION_ITIL_LINK_PATH` und `GLPI_ESCALATION_ITIL_LINK_BODY` konfigurierter POST
- Kategorien: `/api.php/v2.3/Dropdowns/ITILCategory`
- OpenAPI-Prüfung: `/api.php/doc.json`

Die OpenAPI-Dokumentation deiner Installation ist die maßgebliche Quelle, weil GLPI die API-Dokumentation dynamisch aus Core und aktivierten Plugins erzeugt.

## Wissensbasis, Quellen und Kommunikationspolicy

Jede Datei in `knowledge/` ist JSON und trägt eine explizite Herkunft sowie Kommunikations-Metadaten:

```json
{
  "id": "KB-128",
  "title": "GlobalProtect Gateway nicht erreichbar",
  "text": "Beschreibung, Fehlermeldungen, Voraussetzungen ...",
  "answer": "Bitte trennen Sie die bestehende VPN-Verbindung vollständig und starten Sie den VPN-Client anschließend neu.",
  "auto_reply": true,
  "min_score": 0.92,
  "categories": [22],
  "keywords": ["GlobalProtect", "Gateway not responding"],
  "source": "internal-kb",
  "source_uri": "kb://network/vpn/128",
  "language": "de-DE",
  "communication_style": "formal"
}
```

Die aktive Source-Policy wird über die Umgebung festgelegt:

```env
# Normale Knowledge-Suche und mögliche Antwortkandidaten.
KNOWLEDGE_ALLOWED_SOURCES=internal-kb,glpi-kb,vendor-docs

# Ausschließlich für die Kategorisierung; keine Antwortauswahl möglich.
KNOWLEDGE_CATEGORY_SOURCES=internal-category

# Nur diese Teilmenge der normalen Quellen darf eine automatische Antwort auslösen.
KNOWLEDGE_AUTO_REPLY_SOURCES=internal-kb,glpi-kb
```

`KNOWLEDGE_AUTO_REPLY_SOURCES` muss eine Teilmenge von `KNOWLEDGE_ALLOWED_SOURCES` sein. `KNOWLEDGE_CATEGORY_SOURCES` darf dagegen eigene Quellen enthalten. Diese werden indexiert und ausschließlich im ersten, separaten Ollama-Aufruf für die Kategorieanalyse verwendet; Antworttext und HTML werden dabei entfernt. Erst nach dieser Kategorieentscheidung werden die normalen Antwortquellen anhand der wirksamen Kategorie neu gerankt und – sofern keine passende Statusantwort ausgewählt wurde – in einem nachgelagerten Ollama-Aufruf bewertet. Kategorie-KB-IDs sind niemals als Antwort-Knowledge zulässig. Ohne gesetzte Variable entspricht `KNOWLEDGE_CATEGORY_SOURCES` aus Kompatibilitätsgründen `KNOWLEDGE_ALLOWED_SOURCES`. Mit `KNOWLEDGE_CATEGORY_SOURCES=none` kann der Knowledge-Einfluss auf die Kategorisierung deaktiviert werden. Mit `KNOWLEDGE_AUTO_REPLY_SOURCES=none` kann die Quellenfreigabe für Auto-Replies vollständig deaktiviert werden. Ein Knowledge-Dokument ohne `source` führt absichtlich zu einem Startfehler, damit die Herkunft nicht implizit geraten wird.

### Gemeinsame KB-Dateien mit fremden Kategorien

Lokale KB-Dateien dürfen in `categories` neben numerischen GLPI-IDs jetzt auch String-Kategorien aus einer anderen Anwendung enthalten. Die Quelldatei muss dafür nicht verändert werden. Beispiel:

```json
{
  "id": "KB-SEC-ATTCK-AN-0001",
  "categories": ["Security", "MITRE ATT&CK", "Account Access"]
}
```

Empfohlener Standard:

```env
KNOWLEDGE_CATEGORY_MODE=unscoped
KNOWLEDGE_CATEGORY_MAP_FILE=/app/data/knowledge-category-map.json
KNOWLEDGE_IGNORE_GLOBS=
```

`unscoped` lädt auch Artikel mit unbekannten externen Kategorien. Diese Labels werden als `external_categories` im Agenten behalten und für das lexikalische Retrieval mitbenutzt. Solange mindestens eine externe Kategorie nicht auf GLPI abgebildet ist, wird `auto_reply` für diesen Artikel **fail-closed deaktiviert**. Der Artikel bleibt aber für RAG und Klassifizierung verfügbar.

Eine Mapping-Datei kann externe Kategorien ohne Änderung der KB-Dateien auf eine oder mehrere GLPI-ITIL-Kategorie-IDs abbilden:

```json
{
  "Security": 17,
  "Account Access": [2, 17],
  "Microsoft Office": 23,
  "Docker": 31
}
```

Alternativ ist auch `{ "mappings": { ... } }` erlaubt. Mapping-Schlüssel werden ohne Beachtung der Groß-/Kleinschreibung verglichen. Numerische Strings in `categories`, z. B. `"17"`, werden direkt als GLPI-ID verstanden.

Weitere Modi:

- `KNOWLEDGE_CATEGORY_MODE=skip`: Eine Datei mit mindestens einer unbekannten externen Kategorie wird komplett ignoriert.
- `KNOWLEDGE_CATEGORY_MODE=strict`: Eine unbekannte externe Kategorie verhindert den Start. Das entspricht dem alten strengen Verhalten.

Bestimmte gemeinsame Dateien können unabhängig davon per Dateimuster ausgeschlossen werden:

```env
KNOWLEDGE_IGNORE_GLOBS=KB-SEC-ATTCK-*.json,external-only-*.json
```

Im Dashboard werden externe und nicht gemappte Kategorien sowie die Zahl ignorierter Dateien angezeigt.

### GLPI Knowledge Base als echter Connector

Die GLPI-Wissensdatenbank kann jetzt direkt read-only synchronisiert werden. Der Agent ermittelt bei `GLPI_KB_PATH=auto` den lesbaren `KnowbaseItem`-Collection-Endpunkt aus `/api.php/doc.json`. GLPI selbst entscheidet anhand der Rechte des OAuth-Service-Accounts, welche Artikel sichtbar sind.

```env
KNOWLEDGE_ALLOWED_SOURCES=internal-kb,glpi-kb
GLPI_KB_ENABLED=true
GLPI_KB_PATH=auto
GLPI_KB_SYNC_INTERVAL=10m
GLPI_KB_LIMIT=500
GLPI_KB_SOURCE=glpi-kb
```

Erfolgreich synchronisierte Artikel werden normalisiert, lokal unter `DATA_DIR/glpi-kb-cache.json` gecacht und in denselben RAG-Index wie lokale Knowledge-Dokumente aufgenommen. Unveränderte Dokumente behalten ihre gecachten Embeddings; nur neue oder geänderte Artikel werden erneut eingebettet. Fällt GLPI bei einem späteren Start/Sync aus, kann der zuletzt gespeicherte Cache weiter als read-only Wissensstand geladen werden.

GLPI-KB-Auto-Replies sind absichtlich separat gesperrt. Die Grundfreigabe ist jetzt bewusst einfach und entspricht der GLPI-Datenstruktur:

1. **Artikel mit GLPI-Knowledge-Base-Kategorie:** Mindestens eine Artikel-KB-Kategorie muss in `GLPI_KB_AUTO_REPLY_CATEGORY_IDS` enthalten sein.
2. **Artikel ohne GLPI-Knowledge-Base-Kategorie:** `GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true` und die konkrete GLPI-KnowbaseItem-ID muss in `GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS` stehen.
3. **ITIL-/Ticketkategorien geben keinen Artikel frei.** Sie können, soweit GLPI ein Mapping liefert, weiterhin als fachliches Signal für Retrieval, Evidenz und die separate Prüfung „Artikel passt zur effektiven Ticketkategorie“ dienen.

Beispiel für kategorisierte Artikel:

```env
KNOWLEDGE_AUTO_REPLY_SOURCES=internal-kb,glpi-kb
GLPI_KB_AUTO_REPLY=true
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=4,7
GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=false
GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS=
```

Beispiel für einen ausdrücklich freigegebenen Artikel ohne KB-Kategorie:

```env
KNOWLEDGE_AUTO_REPLY_SOURCES=internal-kb,glpi-kb
GLPI_KB_AUTO_REPLY=true
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=4,7
GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true
GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS=1,5
```

Damit sind die kategorisierten Artikel der KB-Kategorien `4` und `7` sowie ausschließlich die unkategorisierten GLPI-Artikel `1` und `5` grundsätzlich freigegeben. Alle weiteren Retrieval-, KI-, Evidenz-, Sprach-, Stil-, Kontext- und Ausführungsprüfungen bleiben unverändert.

`GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS` ist veraltet und wird ignoriert. Alte Konfigurationen sollten den Wert leeren oder die Variable entfernen. Ein alter `glpi-kb-cache.json`, der noch mit der vorherigen ITIL-Freigabelogik erzeugt wurde, wird aus Sicherheitsgründen nicht geladen und beim nächsten erfolgreichen GLPI-KB-Sync im neuen Format ersetzt.

Die Diagnose trennt jetzt zwei Fragen klar:

- **„Artikel darf für Auto-Reply verwendet werden“**: reine Grundfreigabe über KB-Kategorie oder konkrete Artikel-ID.
- **„Artikel passt zur effektiven Ticketkategorie“**: fachliche Eignung auf Basis eines vorhandenen Mappings; fehlt ein Mapping, entscheiden Retrieval, KI-Auswahl und Evidenz.

Optional kann `GLPI_KB_FILTER` gesetzt werden, um die von GLPI gelieferte Collection zusätzlich serverseitig einzuschränken. Die konkrete Filtersyntax und verfügbare Felder stammen aus der OpenAPI-Dokumentation deiner installierten GLPI-Version.

Für die Kommunikation gelten zentrale Vorgaben:

```env
COMMUNICATION_LANGUAGE=de-DE
COMMUNICATION_STYLE=formal
COMMUNICATION_SALUTATION=Guten Tag,
COMMUNICATION_CLOSING=Mit freundlichen Grüßen
COMMUNICATION_SIGNATURE=IT-Service
AI_CONTENT_LABEL_ENABLED=true
```

Ein Auto-Reply ist nur erlaubt, wenn `language` und `communication_style` des freigegebenen Knowledge-Dokuments exakt zur aktiven Policy passen. Das Feld `answer` enthält nur den fachlich freigegebenen Nachrichtentext; Anrede, Grußformel und Signatur werden von der Go-Policy zentral ergänzt. Damit kann das Modell diese Kommunikationsvorgaben nicht überschreiben.

`categories` begrenzt Auto-Reply auf die angegebenen Zielkategorien. Eine leere Liste bedeutet keine zusätzliche Kategorie-Einschränkung. `min_score` kann die globale Schwelle je Artikel verschärfen.

Bei aktiviertem RAG erzeugt Ollama Embeddings über `/api/embed`. Der aktive lokale Index wird persistent unter `DATA_DIR/knowledge-index/snapshot.gob` gespeichert. Ein vorhandenes altes `data/embeddings.json` wird nur noch als einmalige Migrationsquelle verwendet. Für Ticket und Knowledge wird dasselbe Embedding-Modell verwendet.

### Realistisches Hybrid-Scoring und dynamische Kandidatenauswahl

Knowledge-Treffer werden nicht nur über eine einzelne Cosine-Similarity bewertet. Lange Artikel werden in überlappende Abschnitte zerlegt und der beste semantische Abschnitt wird mit Titel-, lexikalischen, Keyword- und Kategorie-/Lernsignalen kombiniert. Empfohlene Standardwerte:

```env
KNOWLEDGE_MIN_SCORE=0.70
KNOWLEDGE_RETRIEVAL_FLOOR=0.30
KNOWLEDGE_WEIGHT_SEMANTIC=0.45
KNOWLEDGE_WEIGHT_TITLE=0.20
KNOWLEDGE_WEIGHT_LEXICAL=0.20
KNOWLEDGE_WEIGHT_KEYWORDS=0.075
KNOWLEDGE_WEIGHT_CATEGORY=0.075
KNOWLEDGE_CHUNK_WORDS=160
KNOWLEDGE_CHUNK_OVERLAP_WORDS=30
KNOWLEDGE_MAX_CHUNKS_PER_DOC=24
KNOWLEDGE_MAX_QUERY_CHUNKS=64

# Dynamisches Top-K
KNOWLEDGE_TOP_K=6
KNOWLEDGE_AUDIT_TOP_K=10
KNOWLEDGE_CANDIDATE_MAX_GAP=0.20
```

`KNOWLEDGE_TOP_K` ist jetzt **die maximale Anzahl von Kandidaten, die Ollama sehen darf**, nicht die Anzahl, die blind immer übergeben wird. Nach dem Retrieval wird ein dynamischer Cutoff berechnet:

```text
cutoff = max(KNOWLEDGE_RETRIEVAL_FLOOR, bester_score - KNOWLEDGE_CANDIDATE_MAX_GAP)
```

Beispiel: Bei Scores `0.82, 0.79, 0.76, 0.43` und `KNOWLEDGE_CANDIDATE_MAX_GAP=0.20` gehen nur die ersten drei Treffer an Ollama, weil der Cutoff `0.62` beträgt. Bei einem unklareren Fall `0.66, 0.64, 0.63, 0.61, 0.59` dürfen dagegen bis zu fünf Kandidaten in den Modellkontext. Liegt bereits der beste Treffer unter `KNOWLEDGE_RETRIEVAL_FLOOR`, erhält Ollama **keinen** KB-Kandidaten.

`KNOWLEDGE_AUDIT_TOP_K` ist davon getrennt. Das Dashboard kann z. B. die besten zehn Treffer zur Diagnose zeigen, während höchstens sechs und meist deutlich weniger an Ollama gesendet werden. Jeder Audit-Kandidat wird mit `an KI gesendet` oder `nur Audit` gekennzeichnet.

Der angezeigte Retrieval-/Hybrid-Score ist **keine Wahrscheinlichkeit**. Er ist ein nachvollziehbarer Ranking-Score. Die Semantik verwendet die Ähnlichkeit des besten Body-Chunks; der Titel kombiniert Embedding- und lexikalischen Titelmatch; Keywords und Kategorie-/Lernsignale dienen als positive Evidenz. Fehlen solche Metadaten, werden sie nicht als Null-Strafe eingerechnet.

Für die spätere Auto-Reply-Freigabe gilt weiterhin die getrennte Evidenzlogik aus Retrieval, KI-Auswahl und Kategorieübereinstimmung. Der effektive finale Schwellwert ist `max(KNOWLEDGE_MIN_SCORE, min_score des Artikels)`.

## Operativer Kontext: Changes, Major Incidents, Uptime Kuma und Geräte

Der Agent kann vor der LLM-Entscheidung zusätzliche **read-only** Betriebsdaten einsammeln. Diese Daten werden normalisiert und als Fakten in den Prompt aufgenommen; das Modell erhält keine direkten Zugangsdaten und keine zusätzlichen Schreibwerkzeuge.

### Change Calendar

```env
CHANGE_CALENDAR_ENABLED=true
GLPI_CHANGE_PATH=/Assistance/Change
GLPI_CHANGE_FILTER=
GLPI_CHANGE_LIMIT=100
CHANGE_LOOKBACK=48h
CHANGE_LOOKAHEAD=24h
```

Der Agent lädt Changes im konfigurierten Zeitfenster, berechnet eine deterministische Relevanz zum Ticket (u. a. Tickettext, verknüpfte Geräte/Standorte) und übergibt höchstens die relevantesten Einträge an Ollama. `GLPI_CHANGE_PATH` wird beim Start gegen `/api.php/doc.json` geprüft. Bei einer Installation mit abweichender Route oder Filter-Syntax muss die Konfiguration an das OpenAPI-Schema der eigenen Instanz angepasst werden.

### Aktive Major Incidents

```env
MAJOR_INCIDENTS_ENABLED=false
GLPI_MAJOR_INCIDENT_FILTER=
GLPI_MAJOR_INCIDENT_LIMIT=20
```

Major Incidents werden bewusst **nicht automatisch aus beliebigen Tickets erraten**. Sie sind eine explizit vom Betreiber definierte Teilmenge der GLPI-Tickets. Erst wenn `GLPI_MAJOR_INCIDENT_FILTER` die in deiner Umgebung gültige Filterdefinition enthält, sollte `MAJOR_INCIDENTS_ENABLED=true` gesetzt werden. Ein zum aktuellen Ticket relevanter Major Incident blockiert standardmäßig einen normalen Auto-Reply; die Kategorieanalyse darf weiterlaufen.

Beispiel-Idee (die konkrete Syntax muss zu deinem GLPI-OpenAPI-Schema passen): ein Filter auf eine dedizierte Kategorie, Priorität/Impact oder ein eigenes Kennzeichen für Major Incidents.

### Aktuelle Störungen mit Uptime Kuma

Für interne Uptime-Kuma-Instanzen ist der authentifizierte Prometheus-Endpunkt der empfohlene Modus:

```env
UPTIME_KUMA_ENABLED=true
UPTIME_KUMA_URL=https://uptime.example.org
UPTIME_KUMA_MODE=metrics
UPTIME_KUMA_API_KEY=CHANGE_ME
UPTIME_KUMA_TIMEOUT=10s
UPTIME_KUMA_MAX_ISSUES=20
```

Der Client liest ausschließlich `/metrics`, verwendet den Uptime-Kuma-API-Key als HTTP-Basic-Auth-Passwort und gibt nur Monitore weiter, die nicht `UP` sind. Der API-Key wird nicht an Ollama übergeben.

Alternativ können bereits veröffentlichte Statusseiten gelesen werden:

```env
UPTIME_KUMA_MODE=status_page
UPTIME_KUMA_STATUS_PAGES=it-services,network
UPTIME_KUMA_INCLUDE_MAINTENANCE=true
```

In diesem Modus liest der Agent `/api/status-page/<slug>` und `/api/status-page/heartbeat/<slug>` und berücksichtigt gepinnte Incidents, DOWN/PENDING-Monitore und optional Wartungen. Dieser Modus eignet sich nur für Informationen, die auf der betreffenden Statusseite ohnehin veröffentlicht werden dürfen.

#### Vordefinierte Antworten bei eindeutiger Störung oder Wartung

Optional kann zwischen Kategorie- und normaler KB-Antwortanalyse eine eigene Uptime-Kuma-Zuordnung aktiviert werden:

```env
CONTEXT_STATUS_REPLY_ENABLED=true
CONTEXT_STATUS_REPLY_MIN_RELEVANCE=0.50
CONTEXT_STATUS_REPLY_MIN_AI_CONFIDENCE=0.80
CONTEXT_STATUS_REPLY_MIN_FINAL_SCORE=0.45
CONTEXT_INCIDENT_REPLY_TEXT=Zu Ihrer Meldung liegt derzeit wahrscheinlich eine zentrale Störung bei {{service_name}} vor. Die Einschränkung kann damit zusammenhängen. Wir beobachten den Status.
CONTEXT_MAINTENANCE_REPLY_TEXT=Für {{service_name}} läuft derzeit eine Wartung. Die von Ihnen beschriebene Einschränkung kann damit zusammenhängen. Bitte testen Sie den Dienst nach Abschluss der Wartung erneut.
```

Der Ablauf ist strikt getrennt:

1. Die Kategorie wird bestimmt.
2. Ollama darf ausschließlich bewerten, ob genau ein aktiver Uptime-Kuma-Eintrag zum Ticket passt. Die strukturierte Ausgabe enthält nur Treffer, Kandidaten-ID, Confidence und eine interne Begründung.
3. Go prüft den deterministischen Relevanzscore, die KI-Confidence und `Relevanz × KI-Confidence`.
4. Nur wenn alle drei Schwellwerte erreicht sind, wird der passende Betreibertext für **Störung** oder **Wartung** verwendet. Die normale KB-Antwortanalyse wird dann übersprungen.
5. Bei Unsicherheit greift weiterhin der normale, fail-closed Reply-Pfad.

Die KI formuliert dabei **keinen** Benutzertext. Folgende Platzhalter werden ausschließlich mit den bereits gelesenen Uptime-Kuma-Daten ersetzt: `{{service_name}}`, `{{status}}`, `{{status_page}}`, `{{message}}`, `{{incident_title}}`, `{{incident_content}}` und `{{last_heartbeat}}`. In ENV-Werten kann `\n` für einen Zeilenumbruch verwendet werden.

`AUTO_REPLY=true`, ein Ticket ohne vorhandenes Followup und ein vollständiger Kontext sind weiterhin zwingend erforderlich.

### Beziehungen zwischen Benutzer und Gerät

```env
USER_DEVICE_CONTEXT_ENABLED=true
GLPI_USER_DEVICE_PATHS=/Assets/Computer
GLPI_USER_DEVICE_FILTER_TEMPLATE=user.id=={{user_id}}
GLPI_USER_DEVICE_LIMIT=20
```

Der Agent nutzt zunächst direkt am Ticket verknüpfte GLPI-Items. Zusätzlich werden – soweit der Ticket-Response Requester-IDs enthält – über die konfigurierten Asset-Routen dem Benutzer zugeordnete Geräte gelesen. Die Pfade werden beim Start gegen die OpenAPI-Dokumentation geprüft; die Filter-Syntax ist installationsabhängig und sollte im Shadow Mode verifiziert werden.

Die normalisierten Gerätedaten dienen u. a. dazu, Changes und Störungen besser zum Ticket zuzuordnen. Es werden keine Assets geändert.

### Fail-closed Verhalten

```env
CONTEXT_ENABLED=true
CONTEXT_TIMEOUT=12s
CONTEXT_RELEVANCE_MIN_SCORE=0.20
CONTEXT_BLOCK_AUTO_REPLY_ON_ERRORS=true
CONTEXT_BLOCK_AUTO_REPLY_ON_INCIDENT=true
```

Mit den sicheren Defaults gilt:

- Fällt eine aktivierte Kontextquelle aus, wird der Lauf als unvollständig markiert und **kein Auto-Reply** gesendet.
- Ein relevanter Major Incident oder eine relevante Uptime-Kuma-Störung blockiert einen normalen Standard-Auto-Reply.
- Ist die optionale Statusantwort aktiviert und erreicht eine Uptime-Kuma-Zuordnung alle konfigurierten Schwellwerte, darf stattdessen ausschließlich der vordefinierte Störungs- oder Wartungstext gesendet werden.
- Kategorieanalyse und Auditierung können trotzdem stattfinden.
- Kontextquellen haben ausschließlich Leserechte.
- Im Dashboard/Audit erscheinen pro Lauf die Anzahl der gefundenen Changes, Incidents, Uptime-Issues und Geräte sowie Warnungen bei unvollständigem Kontext.

## Web- und Monitoring-Endpunkte

- `/` – Dashboard (Basic Auth)
- `/api/status` – Status JSON inklusive aktiver Sprache/Stil- und Quellenpolicy (Basic Auth)
- `/api/runs?limit=50` – letzte Audit-Läufe (Basic Auth)
- `/api/diagnostics/analysis/{analysis_id}` – einzelner, eigenständiger KI-Analyselauf (Basic Auth)
- `/healthz` – Prozess lebt
- `/readyz` – GLPI und Ollama erreichbar
- `/metrics` – Prometheus Textformat
- `POST /webhook/glpi` – optionaler Webhook-Eingang, geschützt durch `X-Webhook-Secret`

Das Polling bleibt immer aktiv und dient als Fallback. Der Webhook-Parser akzeptiert übliche Ticket-ID-Felder sowie Ticket-URLs; prüfe die konkrete Payload deiner GLPI-Webhook-Konfiguration im Shadow Mode.

### Erklärbare Entscheidungen im Dashboard

Für den Shadow-/Einführungsbetrieb speichert jeder Lauf die **rohe KI-Empfehlung** getrennt von der **Policy-Entscheidung**. Das Modell liefert bei Kategorien nur noch `id` und `confidence`; ein eigenes `change=true/false` gibt es nicht mehr. Ob tatsächlich geändert werden darf, entscheidet ausschließlich Go anhand der aktuellen Kategorie, der bekannten GLPI-Kategorien und `CATEGORY_CONFIDENCE`.

Das Dashboard zeigt deshalb unter anderem:

- aktuelle Kategorie mit ID und Name,
- von der KI empfohlene Kategorie mit ID und Name,
- KI-Confidence und konfigurierten Schwellwert,
- expliziten Entscheidungsgrund wie `category_confidence_below_threshold`, `category_already_correct` oder `category_written`,
- KI-Empfehlung für Auto-Reply samt Confidence und Reply-Schwellwert,
- besten Knowledge-Treffer mit Hybrid-Score, Einzelkomponenten, effektivem Schwellwert und bestem Artikelabschnitt,
- den ersten Policy-Blocker für einen Reply, z. B. fehlendes Knowledge, vorhandenes Followup, unvollständigen Kontext oder einen relevanten Incident,
- die fachliche KI-Begründung separat von den technischen Policy-Codes.

Damit ist auch ein Lauf ohne Schreibaktion nachvollziehbar. Beispiel: „KI empfiehlt Active Directory (#17) mit 82 %, Schwellwert 90 % → nicht geändert“. Die JSON-Details stehen zusätzlich unter `/api/runs?limit=50` zur Verfügung.

## Keine Doppelantworten

Der Schreibpfad ist bewusst streng:

1. Ticket laden.
2. Followups laden. Existiert eines: **Stop**.
3. Knowledge sowie read-only Betriebskontext (Changes, Major Incidents, Uptime Kuma, Benutzer/Geräte) laden.
4. KI empfiehlt Kategorie-ID + Confidence und optional einen Knowledge-basierten Reply; Kontextdaten sind nur Fakten, keine ausführbaren Anweisungen.
5. Policy Engine entscheidet deterministisch über Kategorieänderung und Reply und protokolliert jeden akzeptierten oder blockierten Gate-Grund.
6. Optional Kategorie ändern.
7. Direkt vor Auto-Reply Ticket und Followups **erneut** laden. Existiert jetzt ein Followup oder hat sich die Entscheidungsgrundlage geändert: **Stop**.
8. Freigegebenen KB-Antworttext als Followup schreiben.

`GLPI_AGENT_USER_ID` wird bei `AUTO_REPLY=true` absichtlich verlangt, damit die Betreiberkonfiguration eindeutig einem dedizierten GLPI-Konto zugeordnet ist. Der aktuelle Code blockiert bei *jedem* vorhandenen Followup – einschließlich eines früheren Agent-Followups – und ist damit konservativer als nur "fremde" Antworten zu prüfen.

## Produktionshinweise

- Dashboard hinter Reverse Proxy mit TLS betreiben; Compose bindet Port 8080 absichtlich nur an `127.0.0.1`.
- GLPI über HTTPS anbinden. Plain HTTP wird standardmäßig abgelehnt (`GLPI_ALLOW_INSECURE_HTTP=false`).
- `.env` niemals committen; besser Docker/Kubernetes Secrets oder systemd `EnvironmentFile` mit restriktiven Dateirechten verwenden.
- Servicekonto nach Least-Privilege-Prinzip konfigurieren.
- Für mehrere parallele Agent-Replikate muss die lokale Queue/State-Sperre durch einen verteilten Store/Lock (z. B. PostgreSQL/Redis) ersetzt werden. Die mitgelieferte Version ist für **eine aktive Agent-Instanz** ausgelegt.
- Vor Live-Auto-Reply Tests mit echten anonymisierten Ticketmustern durchführen.
- Knowledge-Antworten fachlich freigeben und versionieren.

## Build & Tests

Das Projekt verwendet nur die Go-Standardbibliothek; damit gibt es keine Laufzeit-Abhängigkeiten im Agent-Binary.

```bash
make fmt
make test
make vet
make build
```


## Mehrere Ollama-Nodes

Der Agent unterstützt einen nativen Ollama-Pool mit Least-In-Flight-Routing, Healthchecks, Failover, Modelldigest-Prüfung und Node-Diagnose pro AnalysisRun. Ein einzelnes Modell wird dabei nicht über Rechner verteilt; jeder Node führt vollständige unabhängige Inferenzrequests aus.

```env
OLLAMA_URLS=http://10.20.30.21:11434,http://10.20.30.22:11434,http://10.20.30.23:11434
OLLAMA_NODE_NAMES=lenovo-01,lenovo-02,lenovo-03
OLLAMA_ROUTING_MODE=least_inflight
OLLAMA_NODE_MAX_INFLIGHT=1
OLLAMA_FAILOVER_ENABLED=true
OLLAMA_FAILOVER_ATTEMPTS=0
OLLAMA_REQUIRE_SAME_MODEL_DIGEST=true
WORKERS=3
```

`WORKERS` begrenzt die Zahl gleichzeitig aktiver Ticketpipelines. Für drei gleichartige Nodes sind drei Worker ein sinnvoller Lasttest; die Ressourcen jedes einzelnen Rechners bleiben zusätzlich durch `OLLAMA_NODE_MAX_INFLIGHT=1` geschützt.

Die vollständige Betriebsbeschreibung steht in [OLLAMA-POOL.md](OLLAMA-POOL.md). Der Agent wartet beim Start auf einen kompatiblen Pool, während Dashboard und Node-Diagnose bereits erreichbar bleiben. Bei externen Nodes kann mit `docker compose up -d agent` nur der Agent gestartet werden.

## Docker troubleshooting: `/app/data` permission denied and slow Ollama

The Compose stack contains a one-shot `agent-data-init` service. It prepares the named `agent-data` volume for the non-root agent user before the agent starts. The agent also probes `runs.jsonl` at startup and exits immediately with a clear error if the volume is not writable.

For local LLMs, the default request budget is intentionally longer than a typical HTTP API call:

```env
OLLAMA_TIMEOUT=10m
OLLAMA_NUM_PREDICT=256
OLLAMA_KEEP_ALIVE=10m
OLLAMA_THINK=false
OLLAMA_MAX_CONCURRENT=1
```

`OLLAMA_NUM_PREDICT` limits the maximum generated tokens for the small structured decision. `OLLAMA_KEEP_ALIVE` asks Ollama to keep the analysis model loaded between tickets. `OLLAMA_THINK=false` disables optional model thinking for this deterministic classification task. `OLLAMA_NODE_MAX_INFLIGHT=1` serializes inference on each individual pool node. `OLLAMA_MAX_CONCURRENT` remains a backwards-compatible alias when the new per-node value is not set. On very slow CPU-only hosts, use a smaller local model and/or increase `OLLAMA_TIMEOUT`.

After upgrading an existing Compose deployment, recreate the stack so the init service runs:

```bash
docker compose down
docker compose build --no-cache agent agent-data-init
docker compose up -d
```

You do **not** need to delete `agent-data`; the init service fixes ownership on the existing named volume.

## Human-in-the-loop-Lernen und Web-Knowledge-Base

Der Agent lernt **nicht aus seinen eigenen Entscheidungen**. Im Dashboard kann eine Kategorie eines verarbeiteten Tickets ausdrücklich bestätigt oder korrigiert werden. Diese menschlich bestätigten Beispiele werden in `DATA_DIR/category-learning.json` persistiert und bei ähnlichen Tickets als `confirmed_examples` an das Klassifikationsmodell übergeben. Zusätzlich werden Kategorie-Hinweise aus freigegebenen KB-Keywords und konservativen IT-Semantik-Hinweisen aufgebaut.

Konfiguration:

```env
LEARNING_ENABLED=true
LEARNING_MAX_EXAMPLES=500
LEARNING_EXAMPLES_PER_CATEGORY=5
```

Das Dashboard zeigt außerdem den Status und die Anzahl der synchronisierten GLPI-KB-Artikel. Synchronisierte GLPI-Artikel sind read-only und als GLPI-Sync gekennzeichnet.

Das Dashboard enthält außerdem einen CRUD-Editor für interne Knowledge-Einträge. Er ist absichtlich nur bei authentifiziertem Dashboard aktiv:

```env
WEB_ALLOW_ANONYMOUS=false
KNOWLEDGE_WEB_EDIT_ENABLED=true
```

Web-verwaltete Artikel landen **nicht** im statischen `KNOWLEDGE_DIR`, sondern unter `DATA_DIR/knowledge-managed/`. Dadurch kann `knowledge/` weiterhin read-only aus Git/Image gemountet werden. Statische Artikel werden im Web angezeigt, können dort aber nicht überschrieben oder gelöscht werden. Neue bzw. im Web verwaltete Artikel werden nach dem Speichern sofort in den laufenden Such-/RAG-Store aufgenommen; ein Neustart ist nicht nötig.

Für stabilere Structured Outputs sind die empfohlenen Startwerte:

```env
OLLAMA_NUM_PREDICT=768
OLLAMA_JSON_RETRIES=1
```

Bei unvollständigem/ungültigem JSON wird genau einmal erneut eine schema-konforme Antwort angefordert.

### Deployment mit Gitea Container Registry unter Linux

Für ein bereits in Gitea gebautes Image ist `docker-compose.registry.yml` vorgesehen; lokal wird nichts gebaut.

```bash
export AGENT_IMAGE=gitea.example.de/organisation/glpi-ai-agent:latest
mkdir -p data knowledge
sudo chown 65532:65532 data
# knowledge bleibt absichtlich read-only; Web-KB landet unter data/knowledge-managed/
docker compose -f docker-compose.registry.yml pull
docker compose -f docker-compose.registry.yml up -d
```

Bei neuen Gitea-Builds genügt:

```bash
docker compose -f docker-compose.registry.yml up -d --pull always
```

### Lange Tickets und KB-Artikel

Für die semantische Relevanz werden **beide Seiten** in überlappende Abschnitte zerlegt. Ticket-Abschnitte werden gegen KB-Abschnitte verglichen; der beste lokale Treffer bildet die semantische Komponente. Der Ticket-Betreff wird separat für den Titel-Score verwendet. Dadurch verwässern lange Ticketbeschreibungen einen klar passenden Lösungsabschnitt nicht mehr.

Der Ollama-Embedding-Aufruf verwendet `truncate:false`. Ein Text, der trotz Chunking das Kontextfenster des Embedding-Modells überschreitet, führt damit zu einem sichtbaren Fehler statt zu stiller Kürzung.


## Dashboard / Control Center

Das integrierte Webinterface ist als Betriebs- und Diagnoseoberfläche ausgelegt. Neben Status und Metriken zeigt es die effektiven, nicht geheimen Konfigurationswerte, die einzelnen Policy-Entscheidungen und die Komponenten des Hybrid-RAG-Scores. Eine Verarbeitung kann geöffnet werden, um die Top-KB-Kandidaten, deren Score-Komponenten, die besten Ticket-/KB-Chunks sowie relevante Changes, Incidents, Uptime-Kuma-Störungen und Geräte zu sehen.

Die interne Knowledge Base kann bei `KNOWLEDGE_WEB_EDIT_ENABLED=true` direkt im authentifizierten Dashboard angelegt, bearbeitet und gelöscht werden. Der Editor lädt beim Bearbeiten immer den aktuellen Stand vom Server. Artikel-IDs sind nach der Anlage unveränderlich. Statische und aus GLPI synchronisierte Artikel bleiben read-only.


### GLPI-KB Rich Text
Rich-Text-Formatierungen aus synchronisierten GLPI-KB-Artikeln bleiben in Ticketantworten erhalten; RAG und LLM sehen weiterhin nur bereinigten Plaintext.

### Große Knowledge-Verzeichnisse und persistenter Index

Für große lokale Korpora wird der vollständige lokale Index nach dem ersten erfolgreichen Aufbau persistent unter `DATA_DIR/knowledge-index/snapshot.gob` gespeichert. Beim normalen Neustart mit `KNOWLEDGE_INDEX_MODE=incremental` wird dieser Snapshot zuerst geladen; die Ticketverarbeitung kann anschließend mit dem letzten konsistenten Index starten. Die Quelldateien werden danach im Hintergrund inkrementell geprüft.

Empfohlene Werte:

```env
KNOWLEDGE_INDEX_MODE=incremental
KNOWLEDGE_EMBED_BATCH_SIZE=64
KNOWLEDGE_INDEX_SCAN_INTERVAL=5m
```

Beim Delta-Scan werden zunächst nur Dateiname, Größe und `mtime` geprüft. Unveränderte Dateien werden weder geöffnet noch geparst. Erst bei geänderten Metadaten wird der Dateiinhalt gelesen und gehasht; nur tatsächlich geänderte Retrieval-Inhalte werden erneut an Ollama `/api/embed` geschickt. Gelöschte Dateien werden aus dem Index entfernt.

Index-Modi:

- `incremental`: vorhandenen Snapshot sofort laden und Änderungen im Hintergrund nachziehen. Empfohlen.
- `rebuild`: Quelldateien beim Start vollständig neu einlesen; gültige Embeddings aus dem alten Cache können bei der Migration weiterhin wiederverwendet werden.
- `readonly`: ausschließlich einen vorhandenen persistenten Snapshot verwenden; ohne kompatiblen Snapshot schlägt der Start fehl.

`/api/status` und das Dashboard zeigen unter anderem Snapshot-Zeitpunkt, letzten Delta-Scan, geänderte/gelöschte Dateien, wiederverwendete Vektoren, Embedding-Batchgröße und Scanintervall. Das komplette Vektorindex-Map wird bei einer Suche nicht mehr pro Ticket kopiert; Suchläufe lesen den warmen Index direkt unter einem Read-Lock.

Beim allerersten Aufbau ohne Snapshot startet das Dashboard weiterhin sofort und zeigt Scan-/Embedding-Fortschritt. Die Ticketverarbeitung wartet in diesem Fall, bis der erste konsistente Index fertig ist.


### KI-Kennzeichnung automatischer Antworten

Mit `AI_CONTENT_LABEL_ENABLED=true` wird der konfigurierte TrustedNet-Kennzeichnungsblock unveraendert an den Anfang jeder automatisch vom Agenten ausgewaehlten Antwort gesetzt. Da der Badge HTML verwendet, werden auch Plaintext-KB-Antworten fuer den Versand sicher nach HTML escaped.

## Entscheidungsdiagnose

Neben dem normalen Control Center steht unter `/diagnostics` ein separates Diagnose-Cockpit zur Verfügung. Es verwendet die vom Agenten selbst gespeicherten Policy-Regeln und zeigt pro Ticketlauf u. a.:

- alle eigenständigen Analyseläufe (`category`, `priority`, `status_match`, `reply_selection`, `escalation`) als separat öffnbare Karten,

- alle Kategorie-Gates mit Ist-/Sollwert und Blockierstatus,
- alle Auto-Reply-Gates (Quelle, Sprache, Stil, Artikel-Freigabe, Retrieval-Floor, Evidenz, Kategoriebindung, Kontext),
- Ausführungs-/Race-Protection (Followups, Dry-Run, Ticket-Recheck, GLPI-Write),
- einen sichtbaren dreistufigen Ablauf mit Laufstatus und Dauer für Kategorie-, Uptime-Kuma- und Antwortanalyse,
- eine getrennte Kandidatentabelle für `KNOWLEDGE_CATEGORY_SOURCES`, einschließlich der tatsächlich an die Kategorie-KI gesendeten Artikel,
- eine zweite Kandidatentabelle für Antwort-KBs, die erst nach der Kategorieentscheidung neu gerankt und ausgewählt werden,
- getrennte KI-Begründungen für Kategorie, Statuszuordnung und normale Antwortauswahl,
- eine Uptime-Kuma-Kandidatentabelle mit Relevanz, KI-Confidence, Produktscore, Schwellwerten und dem deterministisch gerenderten Betreibertext,
- die Audit-Auswahlgründe (`sent_to_ai`, `below_retrieval_floor`, `outside_candidate_gap`, `max_candidates_reached`),
- einen KB-Inspector, der wahlweise aus Sicht der Kategorie- oder Antwortanalyse prüft.

Neue Ticketläufe verwenden mindestens die Kategorieanalyse und – sofern nötig – die normale Antwortanalyse. Ist `CONTEXT_STATUS_REPLY_ENABLED=true` und sind aktive Uptime-Kuma-Kandidaten vorhanden, liegt dazwischen ein eigener strukturierter Zuordnungslauf. Dieser Lauf erzeugt keinen Antworttext. Er darf nur einen bereitgestellten Kandidaten auswählen und eine Confidence liefern. Erreicht die Kombination aus deterministischer Relevanz, KI-Confidence und Produktscore alle Schwellwerte, wird das passende vordefinierte Störungs- oder Wartungstemplate verwendet und die normale Antwortanalyse übersprungen. Bei vorhandenen Followups, deaktiviertem Auto-Reply, unvollständigem Kontext oder fehlenden Kandidaten werden die jeweiligen Stufen mit einem expliziten Skip-Grund ausgelassen.

Der KB-Inspector rechnet einen Artikel auf Wunsch gegen den aktuellen Ticketstand neu. Hat sich das Ticket seit dem historischen Lauf verändert, kennzeichnet die UI diese Neu-Bewertung ausdrücklich als nicht historisch identisch. Für neue Läufe sind die gespeicherten Regelchecks die maßgebliche historische Erklärung.

Bei Knowledge-Dateien mit nicht gemappten externen Kategorien zeigt die Diagnose die betreffenden Kategorien explizit an. Im Modus `unscoped` bleibt der Artikel für Retrieval nutzbar; wenn die Kompatibilitätslogik die effektive Auto-Reply-Freigabe deaktiviert hat, wird dies als eigenes fehlgeschlagenes Gate dargestellt.

### Kategorie-Mapping-Editor

Unter `/category-mappings` steht ein eigener Editor für
`KNOWLEDGE_CATEGORY_MAP_FILE` zur Verfügung. Er verbindet externe/String-Kategorien
aus lokalen Knowledge-JSONs mit den aktuell aus GLPI gelesenen ITIL-Kategorien.
Der Editor unterstützt Mehrfachzuordnungen, Verwendungshäufigkeiten, Filter für
nicht zugeordnete und verwaiste Einträge sowie unverbindliche Namensvorschläge.
Schreibzugriff ist nur bei `KNOWLEDGE_WEB_EDIT_ENABLED=true` möglich.

### Prioritätslauf blockiert die Queue nicht

Der optionale Prioritätslauf besitzt ein eigenes Zeitbudget:

```env
PRIORITY_ANALYSIS_TIMEOUT=45s
```

Läuft das Modell in einen Timeout oder liefert es eine nicht verwertbare Antwort, wird ausschließlich der Prioritätslauf mit `priority_ai_failed` beendet. Kategorie- und Antwortverarbeitung laufen weiter. Semantische Inkonsistenzen zwischen expliziten Ticketbelegen und Reason Codes werden lokal normalisiert; dafür wird kein zusätzlicher Ollama-Aufruf gestartet.

## Poll-Diagnose und manuelle Neuanalyse

Das Dashboard zeigt den letzten GLPI-Poll jetzt mit Anzahl der abgerufenen, bereits bekannten, neuen und eingereihten Ticketversionen. Unveränderte, bereits verarbeitete Tickets werden weiterhin nicht automatisch erneut analysiert. Für gezielte Tests steht in der Betriebsdiagnose eine manuelle Neuanalyse per Ticket-ID zur Verfügung; sie erzeugt einen separaten Lauf mit dem Trigger `manual_recheck`.
