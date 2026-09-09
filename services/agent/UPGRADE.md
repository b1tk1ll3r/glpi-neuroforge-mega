# Upgrade: vereinfachte GLPI-KB-Auto-Reply-Freigabe

Die ITIL-basierte Artikelfreigabe wurde entfernt. Vor dem Start sollte die `.env` angepasst werden:

```env
# Nur GLPI-Knowledge-Base-Kategorie-IDs
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=4,7

# Unkategorisierte Artikel nur über konkrete KnowbaseItem-IDs
GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true
GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS=1,5

# Veraltet und wirkungslos
GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS=
```

Ein alter `data/glpi-kb-cache.json` wird wegen der geänderten Sicherheitsregel nicht geladen. Der Agent synchronisiert die GLPI-KB neu und schreibt danach einen Cache mit aktueller Policy-Version und einem Hash der relevanten Freigabekonfiguration. Ändert sich später die Allowlist, wird ein Cache mit der alten Freigabe ebenfalls nicht verwendet.

Die Diagnose trennt jetzt:

- `Artikel darf für Auto-Reply verwendet werden`: Grundfreigabe über KB-Kategorie oder Artikel-ID.
- `Artikel passt zur effektiven Ticketkategorie`: optionale fachliche Prüfung über ein vorhandenes GLPI-Mapping.

Details stehen in `HOTFIX-GLPI-KB-SIMPLE-AUTO-REPLY.md`.

## Hotfix für fehlende Prioritätsläufe

Das vorherige Quellarchiv konnte durch ein zu breites Paket-Ausschlussmuster die Verzeichnisse `cmd/agent` und `internal/agent` verlieren. In diesem Fall enthielten neue Laufdatensätze keine `analyses` und keine Prioritätsfelder. Dieses Paket enthält den vollständigen Quellstand. Bitte den Agenten vollständig ersetzen und neu bauen beziehungsweise eines der neuen Programme aus `dist/` verwenden. Historische Läufe werden nicht rückwirkend ergänzt; erst ein neuer Ticketlauf zeigt die Prioritätsdiagnose. Weitere Einzelheiten stehen in `HOTFIX-PRIORITAET.md`.

# Upgrade-Hinweise

## Upgrade: eigenständige Analyseläufe, Priorisierung und Eskalation

Diese Version erweitert den Audit-Datensatz abwärtskompatibel um `trigger` und `analyses`. Alte Zeilen in `data/runs.jsonl` bleiben lesbar; neue Läufe enthalten zusätzlich eigenständige Analyseobjekte. Beim ersten Start wird keine manuelle Datenmigration benötigt. Aus vorhandenen Auditzeilen wird zusätzlich `DATA_DIR/state-index.json` aufgebaut; diese Datei hält die letzte verarbeitete Version je Ticket und ausgeführte Eskalationsschlüssel unabhängig von der Diagnose-Aufbewahrung fest. Sehr große Auditdateien werden nach einem erfolgreichen Append automatisch auf die konfiguriert vorgehaltenen Läufe kompaktiert. Vor dem Upgrade sollte trotzdem eine Sicherung von `DATA_DIR` erstellt werden.

Empfohlener erster Start:

```env
DRY_RUN=true
PRIORITY_ENABLED=true
AUTO_PRIORITY=false
ESCALATION_ENABLED=false
AUTO_ESCALATION=false
```

Damit entstehen separate Prioritätsanalysen, aber keine neuen GLPI-Schreiboperationen. Nach der Auswertung im Diagnose-Cockpit kann die zeitgesteuerte Eskalation ebenfalls im Shadow Mode aktiviert werden:

```env
ESCALATION_ENABLED=true
AUTO_ESCALATION=false
ESCALATION_SCAN_INTERVAL=15m
ESCALATION_MIN_AGE=4h
GLPI_ESCALATION_FILTER=status.id==1
```

Vor `AUTO_PRIORITY=true` oder `AUTO_ESCALATION=true` sind die Rechte und API-Felder jeder freigegebenen Aktion zu prüfen. Live-Aktionen benötigen zusätzlich `DRY_RUN=false`. Zuweisungen, private Followups, Webhooks und der installationsspezifische Major-Incident-Linkadapter sollten einzeln im Shadow Mode getestet werden. Automatische Herabstufungen sind nicht implementiert. Details stehen in `ESCALATION.md`.

Neue Variablen:

- `PRIORITY_ENABLED`, `AUTO_PRIORITY`, `PRIORITY_CONFIDENCE`, `PRIORITY_MAX_INCREASE`, `PRIORITY_ALLOWED_REASON_CODES`
- `ESCALATION_ENABLED`, `AUTO_ESCALATION`, `ESCALATION_SCAN_INTERVAL`, `ESCALATION_MIN_AGE`, `ESCALATION_MIN_INACTIVITY`, `ESCALATION_ANALYSIS_TIMEOUT`, `ESCALATION_CONFIDENCE`, `ESCALATION_MAX_LEVEL`, `ESCALATION_SLA_RISK_WINDOW`
- `ESCALATION_SERVICE_OWNER_MIN_LEVEL`, `ESCALATION_MANAGER_REVIEW_MIN_LEVEL`, `ESCALATION_MAJOR_INCIDENT_MIN_RELEVANCE`
- `ESCALATION_ALLOWED_REASON_CODES`, `ESCALATION_ALLOWED_ACTIONS` sowie die `ESCALATION_*_GROUP_ID`-/`*_USER_ID`-Ziele
- `ESCALATION_ADD_PRIVATE_FOLLOWUP`, die fünf `ESCALATION_*_NOTE`-Templates und die optionalen `ESCALATION_WEBHOOK_*`-Werte
- `GLPI_ESCALATION_GROUP_PATCH_FIELD`, `GLPI_ESCALATION_USER_PATCH_FIELD`, `GLPI_ESCALATION_ITIL_LINK_PATH`, `GLPI_ESCALATION_ITIL_LINK_BODY`, `GLPI_ESCALATION_FILTER`, `GLPI_ESCALATION_LIMIT`


## Vordefinierte Uptime-Kuma-Statusantworten

Optional kann nach der Kategorieanalyse und vor der normalen KB-Antwortauswahl eine
separate Uptime-Kuma-Zuordnung aktiviert werden. Die KI erzeugt dabei keinen
Benutzertext. Sie liefert ausschließlich `matched`, `candidate_id`, `confidence`
und eine interne Begründung.

```env
CONTEXT_STATUS_REPLY_ENABLED=true
CONTEXT_STATUS_REPLY_MIN_RELEVANCE=0.50
CONTEXT_STATUS_REPLY_MIN_AI_CONFIDENCE=0.80
CONTEXT_STATUS_REPLY_MIN_FINAL_SCORE=0.45
CONTEXT_INCIDENT_REPLY_TEXT=Zu Ihrer Meldung liegt derzeit wahrscheinlich eine zentrale Störung bei {{service_name}} vor.
CONTEXT_MAINTENANCE_REPLY_TEXT=Für {{service_name}} läuft derzeit eine Wartung.
```

Go berechnet `final_score = relevance × ai_confidence`. Nur wenn alle drei
Schwellwerte erreicht werden, wird der passende Betreibertext deterministisch
gerendert und als Followup verwendet. Die normale KB-Antwortanalyse wird dann
übersprungen. Ohne Aktivierung bleibt das bisherige Verhalten unverändert.

Verfügbare Platzhalter sind `{{service_name}}`, `{{status}}`,
`{{status_page}}`, `{{message}}`, `{{incident_title}}`,
`{{incident_content}}` und `{{last_heartbeat}}`.

## Zweistufige Kategorie- und Antwortanalyse

Die bisherige kombinierte Ollama-Entscheidung wurde in zwei echte, aufeinander
folgende Analysen getrennt:

1. Die Kategorieanalyse erhält nur GLPI-Kategorien und Quellen aus
   `KNOWLEDGE_CATEGORY_SOURCES`. Antwortfelder werden entfernt.
2. Die normalen Antwort-KBs werden anschließend anhand der wirksamen Kategorie
   neu gerankt. Nur die ausgewählten Kandidaten gehen an einen zweiten
   Ollama-Aufruf für die Antwortauswahl.

Die Diagnose unter `/diagnostics` zeigt beide Kandidatenlisten, beide
KI-Begründungen, Laufstatus/Dauer und die Kategorie, auf der die Antwortauswahl
beruht. Alte `runs.jsonl`-Einträge bleiben lesbar und werden als historischer
gemeinsamer Lauf gekennzeichnet.

Scheitert nur die zweite Ollama-Stufe, bleibt eine gültige Kategorieentscheidung
erhalten; die Antwort wird fail-closed deaktiviert. Bei vorhandenem Followup,
`AUTO_REPLY=false` oder fehlenden Antwortkandidaten wird die zweite Stufe gar
nicht aufgerufen.

## Kategorisierung ohne auswählbare Antwort

Wenn ein Ticket bereits ein Followup besitzt, `AUTO_REPLY=false` gesetzt ist oder
kein Antwort-Knowledge-Kandidat verfügbar ist, wird ausschließlich die erste
Kategorie-Stufe ausgeführt. Die zweite Antwort-Stufe wird mit einem expliziten
Skip-Grund im Audit ausgelassen und kann die Kategorisierung nicht mehr mit einem
Reply-Fehler abbrechen.

## Getrennte Sources für Kategorisierung und Antworten

Knowledge-Quellen können jetzt getrennt nach Verwendungszweck freigegeben werden:

```env
# Normale Suche und mögliche Antwortkandidaten
KNOWLEDGE_ALLOWED_SOURCES=internal-kb,glpi-kb,runbook

# Ausschließlich Klassifikationswissen
KNOWLEDGE_CATEGORY_SOURCES=internal-category

# Teilmenge der normalen Quellen für automatische Antworten
KNOWLEDGE_AUTO_REPLY_SOURCES=internal-kb,glpi-kb
```

Der Index lädt die Vereinigung aus `KNOWLEDGE_ALLOWED_SOURCES` und
`KNOWLEDGE_CATEGORY_SOURCES`. Einträge, deren Source nur in
`KNOWLEDGE_CATEGORY_SOURCES` steht, werden ausschließlich als Hinweise für die
Kategorieentscheidung verwendet. Ihre Antwortfelder werden nicht an Ollama
übergeben, und ihre IDs können nicht als Antwort-Knowledge ausgewählt werden.

Ohne gesetztes `KNOWLEDGE_CATEGORY_SOURCES` bleibt das bisherige Verhalten
bestehen: Dann werden automatisch die Werte aus `KNOWLEDGE_ALLOWED_SOURCES`
verwendet. Für die mitgelieferten `internal-category`-Dateien sollte die neue
Variable ausdrücklich auf `internal-category` gesetzt werden.

## Neue/empfohlene Variablen

```env
OLLAMA_NUM_PREDICT=768
OLLAMA_JSON_RETRIES=1

LEARNING_ENABLED=true
LEARNING_MAX_EXAMPLES=500
LEARNING_EXAMPLES_PER_CATEGORY=5

# Nur bei authentifiziertem Dashboard aktivieren:
KNOWLEDGE_WEB_EDIT_ENABLED=true
```

`KNOWLEDGE_DIR` bleibt statisch/read-only. Im Dashboard erzeugte Artikel werden automatisch unter `DATA_DIR/knowledge-managed/` gespeichert. Bestätigte Kategorie-Lernbeispiele liegen in `DATA_DIR/category-learning.json`.

## Gitea-Registry / Linux

Das Image weiterhin in Gitea bauen. Auf dem Zielsystem ist kein lokaler Build erforderlich:

```bash
export AGENT_IMAGE=gitea.example.de/organisation/glpi-ai-agent:latest
mkdir -p data knowledge
sudo chown 65532:65532 data
docker compose -f docker-compose.registry.yml up -d --pull always
```

Der statische Ordner `./knowledge` bleibt read-only. Da Web-KB und Lernspeicher unter `./data` liegen, müssen nur die Daten für UID/GID `65532:65532` beschreibbar sein.

## Sicherer Start

Für die ersten Lernläufe empfohlen:

```env
DRY_RUN=true
AUTO_CATEGORY=true
AUTO_REPLY=false
CATEGORY_CONFIDENCE=0.90
```

Im Dashboard anschließend Entscheidungen bestätigen/korrigieren. Erst nach genügend beobachteten Tickets Schwellwerte oder Schreibrechte anpassen.

## GLPI Knowledge Base Connector

Für den neuen read-only GLPI-KB-Sync ergänzen Sie bei Bedarf:

```env
KNOWLEDGE_ALLOWED_SOURCES=internal-kb,glpi-kb
GLPI_KB_ENABLED=true
GLPI_KB_PATH=auto
GLPI_KB_SYNC_INTERVAL=10m
GLPI_KB_SYNC_TIMEOUT=30m
GLPI_KB_LIMIT=500
GLPI_KB_SOURCE=glpi-kb
GLPI_KB_AUTO_REPLY=false
GLPI_KB_AUTO_REPLY_CATEGORY_IDS=
```

Der sichere Start ist `GLPI_KB_AUTO_REPLY=false`. Erst nachdem die importierten Artikel im Dashboard geprüft wurden, sollte `glpi-kb` optional in `KNOWLEDGE_AUTO_REPLY_SOURCES` aufgenommen und eine explizite Whitelist von GLPI-Knowledge-Base-Kategorie-IDs gesetzt werden.

## Hybrid Knowledge Scoring

Diese Version ersetzt den einzelnen Dokument-Cosine-Score durch ein Hybrid-Scoring mit Body-Chunks, Titel, Keywords und Kategorie-/Lernsignalen. Der bestehende `data/embeddings.json` Cache wird bei Bedarf automatisch im neuen Format aufgebaut; ein manuelles Löschen ist nicht erforderlich.

Für bestehende `.env`-Dateien werden folgende Werte empfohlen:

```env
KNOWLEDGE_MIN_SCORE=0.70
KNOWLEDGE_WEIGHT_SEMANTIC=0.50
KNOWLEDGE_WEIGHT_TITLE=0.25
KNOWLEDGE_WEIGHT_KEYWORDS=0.15
KNOWLEDGE_WEIGHT_CATEGORY=0.10
KNOWLEDGE_CHUNK_WORDS=160
KNOWLEDGE_CHUNK_OVERLAP_WORDS=30
KNOWLEDGE_MAX_CHUNKS_PER_DOC=24
```

Der neue Hybrid-Score ist nicht direkt mit alten Cosine-Scores vergleichbar. Nach dem Upgrade zunächst im Dry-Run beobachten und den Mindestscore anhand realer Tickets kalibrieren.

## Dashboard / Knowledge-Editor v2

Das Dashboard wurde grundlegend überarbeitet. Es zeigt jetzt:

- eine Betriebsübersicht mit GLPI-/Ollama-/GLPI-KB-Gesundheit,
- die effektiven, nicht geheimen ENV-Werte gruppiert nach Agent, Ollama, RAG, GLPI-KB und Kontextquellen,
- eine Detailansicht je Verarbeitung mit KI- und Policy-Entscheidung,
- die Top-Knowledge-Kandidaten inklusive Hybrid-, Semantik-, Titel-, Keyword- und Kategorie/Lernscore,
- die tatsächlich verwendeten Ticket-/KB-Chunks,
- kompakte Details zu Changes, Major Incidents, Uptime-Kuma-Störungen und Benutzergeräten,
- Filter für Verarbeitungen, Knowledge Base und Lernbeispiele.

### Geänderte Knowledge-API

Der Webeditor verwendet jetzt explizite CRUD-Semantik:

- `GET /api/knowledge/{id}` lädt einen Artikel frisch vom Server.
- `POST /api/knowledge` legt einen neuen Web-Artikel an und liefert bei einer bereits existierenden ID `409 Conflict`.
- `PUT /api/knowledge/{id}` aktualisiert ausschließlich einen bestehenden, Web-verwalteten Artikel.
- Die ID eines Artikels kann beim Bearbeiten nicht geändert werden.
- `DELETE /api/knowledge/{id}` löscht weiterhin nur Web-verwaltete Artikel.

Statische Git-/Datei-Artikel und synchronisierte GLPI-KB-Artikel bleiben read-only.

Neue Läufe speichern zusätzlich die Top-Knowledge-Kandidaten und kompakte Kontextdetails im Audit. Ältere `runs.jsonl`-Einträge bleiben kompatibel; dort sind diese neuen Detailfelder naturgemäß leer.


## Rich-Text-Antworten aus der GLPI Knowledge Base

Synchronisierte GLPI-KB-Artikel behalten ab dieser Version zwei getrennte Darstellungen:

- `text` / `answer`: bereinigter Plaintext für RAG, Ranking und LLM-Kontext.
- `answer_html`: originales GLPI-Rich-Text-Markup ausschließlich für die spätere Ticketantwort.

Dadurch bleiben bei Auto-Replies unter anderem Überschriften, Fett/Kursiv, Listen, Tabellen und Links erhalten. Das Rich-Text-Markup wird nicht an Ollama gesendet und beeinflusst keine Embeddings. Anrede und Signatur werden HTML-sicher um den KB-Inhalt ergänzt.

Es sind keine neuen ENV-Variablen erforderlich. Nach dem Upgrade führt der initiale GLPI-KB-Sync automatisch dazu, dass `answer_html` im lokalen GLPI-KB-Cache ergänzt wird.


## Dynamisches Knowledge Top-K

Für Installationen mit vielen Knowledge-Artikeln wird die Kandidatenauswahl ab dieser Version dynamisch begrenzt. Empfohlene Werte:

```env
KNOWLEDGE_TOP_K=6
KNOWLEDGE_AUDIT_TOP_K=10
KNOWLEDGE_CANDIDATE_MAX_GAP=0.20
KNOWLEDGE_RETRIEVAL_FLOOR=0.30
```

`KNOWLEDGE_TOP_K` ist die maximale Anzahl von Artikeln im Ollama-Prompt. Artikel werden nur übergeben, wenn sie mindestens den Retrieval-Floor erreichen und nicht mehr als `KNOWLEDGE_CANDIDATE_MAX_GAP` unter dem besten Treffer liegen. `KNOWLEDGE_AUDIT_TOP_K` steuert separat, wie viele Treffer für Dashboard/Audit aufbewahrt werden. Bestehende `.env`-Dateien sollten die drei neuen/angepassten Werte explizit ergänzen.


## Shared KB category compatibility

Local knowledge JSON files may now use external string labels in `categories`. Recommended migration settings:

```env
KNOWLEDGE_CATEGORY_MODE=unscoped
KNOWLEDGE_CATEGORY_MAP_FILE=/app/data/knowledge-category-map.json
KNOWLEDGE_IGNORE_GLOBS=
```

Unmapped labels no longer crash startup in `unscoped` mode. Such documents remain searchable but their `auto_reply` is disabled until all external labels are mapped. Use `skip` to ignore those documents or `strict` to retain fail-fast behavior.

## Große lokale Knowledge Bases (vNext)

Lokale Knowledge-Verzeichnisse werden beim Prozessstart nicht mehr synchron vor dem HTTP-Server indexiert. Das WebUI startet zuerst; Scan, JSON-Validierung, Cache-Prüfung und Embeddings laufen anschließend im Hintergrund.

Währenddessen gilt:

- `/healthz` bleibt erreichbar.
- `/readyz` liefert HTTP 503, bis GLPI, Ollama und die lokale Knowledge Base bereit sind.
- Ticket-Polling und Worker starten erst nach erfolgreicher Knowledge-Initialisierung.
- `/api/status` und das Dashboard zeigen Phase, Datei-/Dokumentfortschritt, Cache-Treffer, offene Embeddings und Fehler.
- Bei einem fehlerhaften KB-Dokument bleibt das WebUI erreichbar und zeigt den Initialisierungsfehler an.

Die Embedding-Erzeugung verarbeitet große Korpora dokumentweise in Batches. Nach dem ersten vollständigen Aufbau wird ein atomarer persistenter Snapshot geschrieben; spätere Starts verwenden diesen Snapshot und führen nur Delta-Scans aus.


## Persistenter inkrementeller Knowledge-Index

Für große lokale KB-Bestände sollte die bestehende `.env` ergänzt werden:

```env
KNOWLEDGE_INDEX_MODE=incremental
KNOWLEDGE_EMBED_BATCH_SIZE=64
KNOWLEDGE_INDEX_SCAN_INTERVAL=5m
```

Der neue Snapshot liegt unter `DATA_DIR/knowledge-index/snapshot.gob`. Bei Docker muss `DATA_DIR` deshalb dauerhaft gemountet und für UID/GID `65532:65532` beschreibbar bleiben. `docker compose down -v` bzw. das Löschen des Host-Verzeichnisses entfernt auch den persistenten Index.

Beim ersten Start dieser Version existiert noch kein Snapshot. Der Agent kann vorhandene gültige Vektoren aus dem bisherigen `DATA_DIR/embeddings.json` übernehmen und schreibt nach erfolgreichem Aufbau den neuen Snapshot. Danach wird `embeddings.json` für die lokale KB nicht mehr als primärer Index benötigt.

Normaler Neustart in `incremental`:

1. Snapshot laden.
2. Knowledge sofort als `ready` markieren.
3. Ticketverarbeitung starten.
4. Quelldateien im Hintergrund per Größe/`mtime` vergleichen.
5. Nur geänderte Dateien lesen/hashen/parsen und nur geänderte Retrieval-Texte neu embedden.
6. Geänderten Snapshot atomar ersetzen.

`KNOWLEDGE_INDEX_MODE=rebuild` erzwingt einen vollständigen Quellen-Scan. `readonly` verwendet ausschließlich den vorhandenen Snapshot und führt keine lokalen Delta-Scans aus.


## KI-Kennzeichnung

Automatische Antworten tragen standardmaessig den TrustedNet-Kennzeichnungsblock am Anfang. Zum expliziten Aktivieren/Deaktivieren: `AI_CONTENT_LABEL_ENABLED=true|false`.

## Diagnose-Cockpit

Nach dem Upgrade ist keine neue ENV-Variable erforderlich. Das neue Interface ist unter `/diagnostics` erreichbar und verwendet dieselbe Web-Authentifizierung wie das normale Dashboard.

Neue Ticketläufe speichern strukturierte `category_checks`, `reply_checks` und `execution_checks` in `runs.jsonl`. Alte Laufdatensätze bleiben kompatibel, enthalten diese historischen Checks naturgemäß jedoch nicht rückwirkend.

## Kategorie-Mapping-Editor

Das bestehende `KNOWLEDGE_CATEGORY_MAP_FILE` kann jetzt über das authentifizierte
Webinterface unter `/category-mappings` gepflegt werden.

Voraussetzungen:

- `KNOWLEDGE_CATEGORY_MAP_FILE` zeigt auf eine für den Agenten beschreibbare Datei,
  empfohlen unter `DATA_DIR`, z. B. `/app/data/knowledge-category-map.json`.
- `KNOWLEDGE_WEB_EDIT_ENABLED=true`.
- Das Webinterface ist in Produktion authentifiziert (`WEB_ALLOW_ANONYMOUS=false`).

Der Editor zeigt alle String-/Fremdkategorien der lokalen indexierten KB-Dateien,
ihre Verwendungshäufigkeit und die aktuell aus GLPI gelesenen ITIL-Kategorien.
Eine Fremdkategorie kann mehreren GLPI-Kategorien zugeordnet werden. Verwaiste
Mappings bleiben sichtbar und können bewusst entfernt werden.

Beim Speichern wird die Mapping-Datei atomar ersetzt. Anschließend werden die
lokalen KB-Dateien hinsichtlich Kategoriezuordnung und Policy neu bewertet.
Vorhandene Embeddings werden wiederverwendet, weil Kategorie-Mappings den an das
Embedding-Modell gesendeten Text nicht verändern.

## Prioritäts-Hotfix: neutrale Enthaltungsgründe

Nach diesem Update werden doppelte KI-Reason-Codes normalisiert. Eine unveränderte
Prioritätsempfehlung mit `insufficient_information` erscheint nicht mehr als
`priority_reason_not_allowed`, sondern als
`priority_no_change_insufficient_information`. Es ist keine neue ENV-Variable
erforderlich. Historische Läufe bleiben unverändert; die neue Semantik gilt für
neu ausgeführte Prioritätsanalysen.

## Upgrade auf Prioritäts-Prompt `priority-v3`

Es sind keine neuen Pflichtvariablen erforderlich. `OLLAMA_JSON_RETRIES=1` wird empfohlen, damit eine inkonsistente erste Modellausgabe einmal mit dem konkreten Validierungsfehler erneut angefordert werden kann.

Nach dem Neustart gelten nur neue Ticketläufe als `priority-v3`. Historische Auditdaten werden nicht verändert.

## Emergency-Hotfix priority-v4

`priority-v3` konnte bei semantisch widersprüchlichen Modellantworten einen Wiederholungsaufruf erzeugen. Mit nur einem parallelen Ollama-Aufruf konnte dies die gesamte Ticketpipeline bis zum allgemeinen Ollama-Timeout verzögern.

Neu:

```env
PRIORITY_ANALYSIS_TIMEOUT=45s
```

Der Prioritätslauf ist nun strikt fail-open. Semantische Widersprüche werden deterministisch normalisiert und führen nicht mehr zu einem weiteren Modellaufruf. Beim Austausch des Releases `data/`, `knowledge/` und lokale Umgebungsdateien beibehalten.

## Upgrade auf mehrere Ollama-Nodes

Die bisherige Einzelnode-Konfiguration bleibt kompatibel:

```env
OLLAMA_URL=http://localhost:11434
OLLAMA_URLS=
```

Für einen Pool ergänzen Sie mindestens:

```env
OLLAMA_URLS=http://10.20.30.21:11434,http://10.20.30.22:11434,http://10.20.30.23:11434
OLLAMA_NODE_NAMES=lenovo-01,lenovo-02,lenovo-03
OLLAMA_ROUTING_MODE=least_inflight
OLLAMA_NODE_MAX_INFLIGHT=1
OLLAMA_FAILOVER_ENABLED=true
OLLAMA_FAILOVER_ATTEMPTS=0
OLLAMA_REQUIRE_SAME_MODEL_DIGEST=true
OLLAMA_REQUIRE_EMBEDDING_MODEL=true
```

Vor dem ersten Start müssen `OLLAMA_MODEL` und – bei aktivem RAG – `OLLAMA_EMBEDDING_MODEL` auf jedem Node vorhanden sein. Bei aktivierter Digest-Pflicht führt bereits ein abweichender Modellstand dazu, dass der Pool keine Requests annimmt. Das Dashboard zeigt pro Node Erreichbarkeit, Kompatibilität, Digest, Auslastung, Fehler und Laufzeit.

Bestehende `runs.jsonl`-Einträge bleiben lesbar. Nur neue `AnalysisRun`-Datensätze enthalten den Bereich `provider` mit Node-Auswahl und Failover-Versuchen. `state-index.json` muss beim Upgrade erhalten bleiben.
