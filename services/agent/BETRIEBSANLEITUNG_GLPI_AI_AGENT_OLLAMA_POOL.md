# Betriebsanleitung – GLPI AI Agent

**Dokumentstand:** 3. August 2026  
**Technische Basis:** Projektstand `glpi-ai-agent-ollama-pool`  
**Zielgruppe:** Betrieb, Administration, Service Desk, Informationssicherheit und technische Projektverantwortliche

> Diese Anleitung beschreibt den tatsächlich vorliegenden Quellstand. Sie trennt bewusst zwischen **Code-Defaults** und den teilweise deutlich offensiveren **Beispielwerten in `.env.example`**. Für eine neue Installation sind die Code-Defaults sicherer; für den produktiven Betrieb muss jede schreibende Funktion schrittweise im Shadow Mode validiert werden.

## Inhaltsverzeichnis

1. [Zweck und Systemgrenzen](#1-zweck-und-systemgrenzen)
2. [Architektur und Datenfluss](#2-architektur-und-datenfluss)
3. [Funktionsübersicht und Auswirkungen](#3-funktionsübersicht-und-auswirkungen)
4. [Sicherheits- und Policy-Modell](#4-sicherheits--und-policy-modell)
5. [Installation und Start](#5-installation-und-start)
6. [Empfohlene Inbetriebnahme](#6-empfohlene-inbetriebnahme)
7. [Regelbetrieb](#7-regelbetrieb)
8. [Persistenz, Backup, Reset und Wiederherstellung](#8-persistenz-backup-reset-und-wiederherstellung)
9. [Diagnose, Endpunkte und Monitoring](#9-diagnose-endpunkte-und-monitoring)
10. [Eskalation im Detail](#10-eskalation-im-detail)
11. [Priorisierung im Detail](#11-priorisierung-im-detail)
12. [Knowledge/RAG und automatische Antworten](#12-knowledgerag-und-automatische-antworten)
13. [Vollständige ENV-Referenz](#13-vollständige-env-referenz)
14. [Fehlerbehebung](#14-fehlerbehebung)
15. [Bekannte Grenzen und Abweichungen](#15-bekannte-grenzen-und-abweichungen)
16. [Betriebs-Checklisten](#16-betriebs-checklisten)

---

# 1. Zweck und Systemgrenzen

Der GLPI AI Agent liest Tickets aus GLPI 11 über die High-Level API, sammelt freigegebene Kontextdaten, führt mehrere voneinander getrennte KI-Analysen über einen oder mehrere Ollama-Nodes aus und übergibt die Ergebnisse an deterministische Go-Policies. Erst die Policy entscheidet, ob eine GLPI-Aktion zulässig ist.

Das Modell besitzt **keinen direkten GLPI-Werkzeugzugriff**. Es kann daher weder eigenständig Kategorien ändern noch Followups schreiben, Prioritäten setzen, Gruppen zuweisen oder Tickets verknüpfen. Es liefert ausschließlich strukturierte Empfehlungen.

Der Agent ist für folgende Hauptaufgaben ausgelegt:

- neue oder geänderte Tickets erkennen und deduplizieren;
- Kategorie aus dem aktuellen GLPI-Katalog auswählen;
- Priorität, Impact, Urgency, Betroffenheitsumfang und Zeitkritikalität analysieren;
- aktive Störungen oder Wartungen aus Uptime Kuma einem Ticket zuordnen;
- einen bereits menschlich erstellten und freigegebenen Knowledge-Artikel als Antwort auswählen;
- offene Tickets unabhängig von `date_mod` zeitgesteuert auf Eskalationsbedarf prüfen;
- Kategorie-, Prioritäts-, Antwort- und Eskalationsentscheidungen vollständig auditieren;
- menschlich bestätigte Kategoriekorrekturen als begrenzte Lernbeispiele speichern;
- lokale und GLPI-interne Knowledge-Inhalte indexieren und verwalten.

Nicht vorgesehen ist eine freie, vom Modell formulierte Endnutzerantwort. Der Inhalt einer automatischen Antwort stammt aus einem freigegebenen Knowledge-Dokument oder aus einer fest konfigurierten Statusvorlage.

# 2. Architektur und Datenfluss

## 2.1 Komponenten

| Komponente | Aufgabe |
|---|---|
| GLPI High-Level API | Tickets, Kategorien, Followups, Knowledge, Changes, Assets und Schreiboperationen |
| Ollama Pool Router | Healthchecks, Routing, per-Node-Auslastungsgrenzen, Digest-Prüfung und Failover |
| Ollama Chatmodell je Node | Strukturierte Kategorie-, Prioritäts-, Status-, Antwort- und Eskalationsempfehlungen |
| Ollama Embeddingmodell je Node | Semantische Vektoren für Hybrid-Retrieval |
| Knowledge Store | Lokale JSON-Artikel, Web-verwaltete Artikel, GLPI-KB-Cache und persistenter Vektorindex |
| Kontextkollektor | Changes, Major Incidents, Requester-Geräte und Uptime-Kuma-Daten |
| Policy | Deterministische Freigabe oder Blockade jeder Aktion |
| Prioritätsqueue | Manuelle Läufe, Webhooks, Polling und Eskalationsscheduler mit getrennten Prioritäten |
| State Store | Audit in `runs.jsonl` und dauerhafte Deduplizierung in `state-index.json` |
| Weboberfläche | Dashboard, Diagnose, Knowledge-Verwaltung, Lernen, Mapping und manuelle Neuanalyse |

## 2.2 Ollama-Pool

Der Agent kann einen Einzelnode oder mehrere unabhängige Ollama-Server verwenden. Jeder Node lädt das vollständige Chat- und Embedding-Modell lokal. Der Pool teilt daher **kein einzelnes Modell und keinen RAM über mehrere Rechner**, sondern verteilt vollständige Inferenzrequests. Das erhöht Gesamtdurchsatz und Verfügbarkeit.

Für jeden KI-Lauf wählt der Router einen gesunden, kompatiblen Node. Standard ist `least_inflight`: Der Node mit den wenigsten laufenden Requests wird bevorzugt; bei gleicher Auslastung gleicht der Router auch die bisherige Requestzahl aus. Retryfähige Netzwerk-, Timeout-, Rate-Limit-, 5xx- oder Response-JSON-Fehler können auf einem anderen Node wiederholt werden. Die Node-Auswahl und jeder Versuch werden im separaten `AnalysisRun.provider` gespeichert.

Bei `OLLAMA_REQUIRE_SAME_MODEL_DIGEST=true` arbeitet der Pool fail-closed, sobald erreichbare Nodes unterschiedliche Chat- oder erforderliche Embedding-Digests melden. Dadurch wird verhindert, dass identische Tickets zufällig mit unterschiedlichen Modellständen bewertet werden.

Beim Prozessstart bleibt die Weboberfläche erreichbar, während der Agent wiederholt auf mindestens einen kompatiblen Node wartet. Knowledge-Initialisierung, Polling und Worker beginnen erst anschließend. Dadurch wird ein noch bootender externer Node nicht zu einem einmaligen dauerhaften Initialisierungsfehler.

## 2.3 Normaler Ticketlauf

1. Der Poller lädt bis zu `GLPI_POLL_LIMIT` Tickets mit `GLPI_TICKET_FILTER`.
2. Aus entscheidungsrelevanten Ticketfeldern wird eine `source_version` gebildet.
3. `state-index.json` entscheidet, ob genau diese Ticketversion bereits verarbeitet wurde.
4. Neue Versionen werden in die Queue gestellt.
5. Ein Worker lädt Ticket und Followups erneut und prüft den erlaubten Status.
6. Kategorie-Knowledge und GLPI-Kategorien werden als Kandidaten vorbereitet.
7. Die Kategorie-KI läuft als eigener `AnalysisRun`.
8. Die Policy prüft Kategorie-ID, Confidence und Änderungsbedarf.
9. Die Prioritäts-KI läuft optional als eigener, fail-open begrenzter `AnalysisRun`.
10. Der Kontextkollektor lädt aktivierte Betriebsdaten.
11. Optional wird eine aktive Uptime-Kuma-Störung oder Wartung zugeordnet.
12. Antwort-Knowledge wird nach der effektiven Kategorie neu gerankt.
13. Die Antwort-KI darf ausschließlich einen bereitgestellten Knowledge-Kandidaten auswählen oder ablehnen.
14. Vor jedem Write werden Ticket und Followups erneut geprüft.
15. Der übergeordnete Lauf und alle Analyseläufe werden persistiert.

## 2.4 Queue-Prioritäten

| Trigger | Priorität | Wirkung |
|---|---:|---|
| `manual_recheck` / manuell | 100 | Höchste Priorität; kann bekannte Ticketversion einmalig erzwingen |
| `webhook` | 80 | Schnelle Reaktion auf GLPI-Ereignisse |
| `poll` | 50 | Reguläre neue/geänderte Tickets |
| `scheduled_escalation` | 20 | Niedrigste Priorität, damit neue Tickets Vorrang haben |

Die Queue dedupliziert nach `Ticket-ID + Trigger`. Ein Poll- und ein Eskalationsauftrag für dasselbe Ticket können deshalb gleichzeitig existieren, zwei Poll-Aufträge jedoch nicht.

# 3. Funktionsübersicht und Auswirkungen

## 3.1 Ticket-Polling und Webhook

**Polling** läuft sofort nach Start der Ticketverarbeitung und anschließend in `GLPI_POLL_INTERVAL`. Die API-Abfrage kann serverseitig gefiltert werden; unabhängig davon prüft die lokale Policy `GLPI_ALLOWED_STATUS_IDS`.

**Webhook** ist nur aktiv, wenn `WEBHOOK_SECRET` gesetzt ist. Der Endpunkt `POST /webhook/glpi` erwartet den Header `X-Webhook-Secret`. Er extrahiert eine Ticket-ID aus mehreren üblichen JSON-Formen oder einer `/Ticket/{id}`-Zeichenfolge und stellt das Ticket mit höherer Queue-Priorität ein. Der Webhook umgeht die Versionserkennung nicht; ein unverändertes, bereits verarbeitetes Ticket kann später als `already_processed` enden.

## 3.2 Automatische Kategorisierung

Die Kategorieanalyse erhält nur bekannte GLPI-Kategorien und eine begrenzte Auswahl an Kategorie-Knowledge. Eine empfohlene ID muss im geladenen GLPI-Katalog existieren. `AUTO_CATEGORY=true` erlaubt die Policy-Prüfung; `DRY_RUN=true` simuliert den Write. Kategorie-Knowledge aus `KNOWLEDGE_CATEGORY_SOURCES` ist niemals als Endnutzerantwort zulässig.

**Auswirkung im Livebetrieb:** `PATCH` des Ticketfeldes für die ITIL-Kategorie. Vor dem Write wird geprüft, ob das Ticket seit der Analyse unverändert ist.

## 3.3 KI-Priorisierung

Die Prioritätsanalyse ist ein separater Lauf. Das Modell empfiehlt GLPI-Priorität 1–6 sowie Impact, Urgency, Scope, Zeitkritikalität und Reason Codes. Explizite Ticketbelege wie „mehrere Benutzer“ oder „Ausweichmöglichkeit vorhanden“ werden zusätzlich deterministisch erkannt.

Die Policy:

- erlaubt keine automatische Herabstufung;
- begrenzt die Erhöhung auf `PRIORITY_MAX_INCREASE` je Ticketlauf;
- verlangt bei einer Erhöhung Mindest-Confidence und einen erlaubten Reason Code;
- behandelt neutrale Gründe wie `insufficient_information` als „keine Änderung“;
- beendet nur den Prioritätslauf bei Timeout oder Modellfehler; Kategorie und Antwort laufen weiter.

**Auswirkung im Livebetrieb:** Priorität des Tickets wird auf den policy-begrenzten Zielwert gesetzt. Impact und Urgency werden derzeit diagnostiziert, aber nicht separat geschrieben.

## 3.4 Operational Context

Der Kontextkollektor kann folgende Quellen zusammenführen:

- GLPI Change Calendar innerhalb von Lookback/Lookahead;
- explizit gefilterte Major-Incident-Tickets;
- Geräte/Assets des Requesters;
- Uptime-Kuma-Störungen und Wartungen.

Bei `CONTEXT_BLOCK_AUTO_REPLY_ON_ERRORS=true` arbeitet die Antwortpolicy fail-closed: Fehler einer aktivierten Kontextquelle können automatische Antworten blockieren. `CONTEXT_BLOCK_AUTO_REPLY_ON_INCIDENT=true` blockiert normale Knowledge-Antworten bei einem relevanten Incident.

## 3.5 Statusbezogene vordefinierte Antworten

Ist `CONTEXT_STATUS_REPLY_ENABLED=true`, darf die KI nur einen aktiven Uptime-Kuma-Kandidaten auswählen. Der Text stammt ausschließlich aus `CONTEXT_INCIDENT_REPLY_TEXT` oder `CONTEXT_MAINTENANCE_REPLY_TEXT`. Die Freigabe erfordert gleichzeitig:

- ausreichende deterministische Relevanz;
- ausreichende KI-Confidence;
- ausreichenden Produktscore `Relevanz × Confidence`;
- vollständigen Kontext;
- einen tatsächlich bekannten Kandidaten.

Bei erfolgreicher Statusantwort wird die normale Knowledge-Antwortanalyse übersprungen.

## 3.6 Knowledge Retrieval und Auto-Reply

Das Retrieval kombiniert Semantik, Betreff/Titel, lexikalische Übereinstimmung, Keywords und Kategorie-/Lernsignale. Lange Tickets und Artikel werden in überlappende Chunks zerlegt. Der Agent schickt nur dynamisch ausgewählte Kandidaten an das Modell.

Eine automatische Antwort benötigt unter anderem:

- `AUTO_REPLY=true` und `DRY_RUN=false` für einen echten Write;
- keine vorhandenen Followups;
- einen vom Modell ausgewählten Kandidaten;
- ausreichende KI-Confidence;
- zulässige Source;
- `auto_reply=true` am Dokument;
- passende Sprache und Kommunikationsstil;
- Retrieval-Floor und finale Evidenz;
- passende effektive Ticketkategorie;
- keine blockierende Kontextlage;
- eine zweite Followup-Prüfung unmittelbar vor dem Write.

**Auswirkung im Livebetrieb:** öffentlicher GLPI-Followup mit festem Knowledge-Inhalt, Anrede, Schlussformel und Signatur.

## 3.7 GLPI Knowledge Base Connector

Der Connector synchronisiert sichtbare GLPI-KB-Artikel periodisch. Rich Text bleibt für den Versand erhalten, während RAG und Modell bereinigten Plaintext sehen. Ein lokaler Cache (`glpi-kb-cache.json`) erlaubt den Start mit dem zuletzt synchronisierten Stand, wenn die initiale GLPI-KB-Abfrage ausfällt.

## 3.8 Knowledge-Webeditor und Kategorie-Mapping

Bei authentifiziertem Dashboard und `KNOWLEDGE_WEB_EDIT_ENABLED=true` können agenteneigene Knowledge-Dokumente unter `DATA_DIR/knowledge-managed/` erstellt, geändert und gelöscht werden. Statische Dateien im `KNOWLEDGE_DIR` und synchronisierte GLPI-Artikel bleiben read-only.

Der Mapping-Editor verbindet externe String-Kategorien aus Knowledge-Dateien mit numerischen GLPI-ITIL-Kategorien. Die Änderungen werden in `KNOWLEDGE_CATEGORY_MAP_FILE` gespeichert und in den laufenden Index übernommen.

## 3.9 Human-in-the-loop-Lernen

Der Agent lernt nur aus ausdrücklich bestätigten oder korrigierten Beispielen, nicht automatisch aus seinen eigenen Entscheidungen. Die Beispiele beeinflussen spätere Kategorieprompts und Retrievalsignale. Die Datei liegt unter `DATA_DIR/category-learning.json`.

## 3.10 Zeitgesteuerte Eskalation

Die Eskalation besitzt einen eigenen Scheduler und ignoriert die normale Ticketversions-Deduplizierung. Sie prüft alte Tickets auch dann, wenn `date_mod` unverändert ist. Ein Lauf kann bis zu drei Aktionen empfehlen. Jede Aktion wird einzeln geprüft und auditiert.

Unterstützte Aktionen:

| Aktion | Live-Auswirkung |
|---|---|
| `raise_priority` | Priorität genau um eine Stufe erhöhen, maximal 6 |
| `assign_second_level` | konfigurierte Second-Level-Gruppe zu vorhandenen Gruppen hinzufügen |
| `assign_security_team` | konfigurierte Security-Gruppe hinzufügen; nur bei `security_incident_suspected` |
| `notify_service_owner` | konfigurierte Gruppe/Person hinzufügen und optional Webhook senden |
| `link_major_incident` | Ticket über installationsspezifischen API-Adapter mit relevantestem Major Incident verknüpfen |
| `request_manager_review` | konfigurierte Gruppe/Person hinzufügen und optional Webhook senden |

Zu jeder erfolgreichen Aktion kann ein privater Followup mit einer festen Vorlage geschrieben werden. Erfolgreiche Aktionsschritte werden je Ticket, Stufe, Aktion und Ziel in `state-index.json` dedupliziert.

## 3.11 Ollama-Pool, Routing und Failover

**Auswirkung:** Mehrere Tickets oder voneinander unabhängige Analyseläufe können über mehrere Rechner parallel verarbeitet werden. Die Geschwindigkeit eines einzelnen Requests bleibt durch den ausgewählten Node begrenzt. Fällt ein Node aus, kann ein noch nicht akzeptierter Inferenzrequest auf einem anderen kompatiblen Node fortgesetzt werden.

Der Pool unterstützt `least_inflight`, `round_robin`, `weighted` und `fastest_recent`. Für gleichartige Lenovo-Systeme mit integrierter GPU ist `least_inflight` zusammen mit `OLLAMA_NODE_MAX_INFLIGHT=1` der empfohlene Start. Für einen später ergänzten leistungsfähigeren GPU-Server kann `weighted` verwendet werden.

# 4. Sicherheits- und Policy-Modell

## 4.1 Schalterhierarchie

| Bereich | Analyse aktiv | Write-Freigabe | Globaler Write-Schalter |
|---|---|---|---|
| Kategorie | immer im normalen Lauf | `AUTO_CATEGORY=true` | `DRY_RUN=false` |
| Antwort | Kandidatenlage und Followup-Status | `AUTO_REPLY=true` | `DRY_RUN=false` |
| Priorität | `PRIORITY_ENABLED=true` | `AUTO_PRIORITY=true` | `DRY_RUN=false` |
| Eskalation | `ESCALATION_ENABLED=true` | `AUTO_ESCALATION=true` | `DRY_RUN=false` |

`DRY_RUN=true` überstimmt alle Auto-Schalter und simuliert freigegebene Aktionen.

## 4.2 Race-Schutz

- Pro Ticket existiert innerhalb eines Prozesses ein Mutex.
- Ticket und Followups werden vor der Analyse geladen.
- Vor einem Live-Write werden entscheidungsrelevanter Ticketzustand und Followups erneut geladen.
- Ändert sich die `source_version`, wird die Aktion abgebrochen.
- GLPI-Schreibfehler werden nicht blind wiederholt.

Eine vollständig atomare „prüfen und schreiben“-Operation kann ohne serverseitigen Conditional Write dennoch nicht garantiert werden.

## 4.3 Rechteprinzip

Das GLPI-Servicekonto sollte nur die tatsächlich aktivierten Rechte besitzen:

- Lesen von Tickets, Kategorien und Followups;
- Kategorie ändern nur bei Live-Kategorisierung;
- öffentliche Followups schreiben nur bei Auto-Reply;
- Priorität ändern nur bei Live-Priorität oder `raise_priority`;
- private Followups schreiben nur bei Eskalationsnotizen;
- Gruppen/Benutzer zuweisen nur bei entsprechenden Eskalationsaktionen;
- ITIL-Verknüpfungen erstellen nur bei `link_major_incident`.

# 5. Installation und Start

## 5.1 Native Windows-Installation

1. Archiv in ein dauerhaftes Verzeichnis entpacken.
2. `.env.example` nach `.env` kopieren.
3. Für native Ausführung verwenden:

```env
DATA_DIR=./data
KNOWLEDGE_DIR=./knowledge
OLLAMA_URL=http://localhost:11434
HTTP_ADDR=:7080
```

4. Modelle installieren:

```powershell
ollama pull qwen3:8b
ollama pull embeddinggemma
```

5. Start über `run.ps1` oder die vorgebaute EXE. `run.ps1` lädt `.env`, korrigiert alte Docker-Pfade und startet derzeit mit `go run ./cmd/agent`. Für einen reinen Binary-Betrieb kann die EXE direkt gestartet werden, nachdem die Variablen im Prozess beziehungsweise Dienst gesetzt wurden.

## 5.2 Docker Compose

Die aktuelle Projektfassung enthält mehrere Compose-Varianten. Vor dem Start müssen Listener und Port-Mapping zusammenpassen:

- `compose_local.yml` mappt `7080:7080`; dazu passt `HTTP_ADDR=:7080`.
- `docker-compose.yml` mappt `127.0.0.1:8080:8080`; dazu muss `HTTP_ADDR=:8080` gesetzt werden **oder** das Mapping auf `127.0.0.1:7080:7080` geändert werden.
- `AGENT_PORT` wird in den vorliegenden Compose-Dateien nicht ausgewertet.

Startbeispiel:

```bash
docker compose -f compose_local.yml up -d ollama
docker compose -f compose_local.yml exec ollama ollama pull qwen3:8b
docker compose -f compose_local.yml exec ollama ollama pull embeddinggemma
docker compose -f compose_local.yml up -d
```

## 5.3 Registry-Deployment

```bash
export AGENT_IMAGE=gitea.example.de/organisation/glpi-ai-agent:2026-08-02
docker compose -f docker-compose.registry.yml pull
docker compose -f docker-compose.registry.yml up -d
```

Das bind-mountete Datenverzeichnis muss für UID/GID des Containers schreibbar sein. Das Knowledge-Verzeichnis darf read-only sein; Web-verwaltete Artikel liegen im Datenverzeichnis.

## 5.4 systemd

Die mitgelieferte Unit erwartet:

- Binary unter `/opt/glpi-ai-agent/glpi-ai-agent`;
- Arbeitsverzeichnis `/opt/glpi-ai-agent`;
- ENV-Datei `/etc/glpi-ai-agent.env`;
- schreibbares Datenverzeichnis unter `/var/lib/glpi-ai-agent`.

Die Pfade in der ENV müssen dazu passen, insbesondere `DATA_DIR=/var/lib/glpi-ai-agent` und ein lesbares `KNOWLEDGE_DIR`.

# 6. Empfohlene Inbetriebnahme

## Phase 1 – reine Analyse

```env
DRY_RUN=true
AUTO_CATEGORY=true
AUTO_REPLY=false
PRIORITY_ENABLED=true
AUTO_PRIORITY=false
ESCALATION_ENABLED=false
AUTO_ESCALATION=false
```

Prüfen: Kategorien, Kandidaten, Reason Codes, Mappingwarnungen, Kontextfehler und Laufzeiten.

## Phase 2 – Eskalation im Shadow Mode

```env
ESCALATION_ENABLED=true
AUTO_ESCALATION=false
GLPI_ESCALATION_FILTER=status.id==1
ESCALATION_SCAN_INTERVAL=30m
ESCALATION_MIN_AGE=4h
ESCALATION_MIN_INACTIVITY=2h
```

Prüfen: gefundene Kandidaten, Inaktivitätsberechnung, SLA-Felder, Zuweisungen, vorgeschlagene Stufen und Aktionen.

## Phase 3 – Kategorie live

```env
DRY_RUN=false
AUTO_CATEGORY=true
AUTO_REPLY=false
AUTO_PRIORITY=false
AUTO_ESCALATION=false
```

## Phase 4 – einzelne Eskalationsaktion live

Zunächst nur:

```env
ESCALATION_ALLOWED_ACTIONS=none,raise_priority
AUTO_ESCALATION=true
```

Danach einzeln Second-Level, Security, Service Owner, Management und zuletzt Major-Incident-Link aktivieren.

## Phase 5 – Auto-Reply

Nur freigegebene Sources und Artikel verwenden. Vorher `GLPI_AGENT_USER_ID`, Kommunikationspolicy, Kategoriebindung, Kontextquellen und zweite Followup-Prüfung im Shadow Mode kontrollieren.

# 7. Regelbetrieb

## 7.1 Tägliche Kontrollen

- `/readyz` liefert HTTP 200.
- Dashboard zeigt GLPI, Ollama und Knowledge als bereit.
- Letzter Poll ist aktuell und `poll_last_error` leer.
- Queue bleibt im Normalbetrieb nahe 0.
- Fehlerzähler steigt nicht dauerhaft.
- Neue Runs erscheinen bei geänderten Tickets.
- GLPI-KB-Sync ist aktuell, wenn aktiviert.
- Eskalationsaktionen und private Notizen stimmen fachlich.

## 7.2 Manuelle Neuanalyse

Im Dashboard oder per API:

```http
POST /api/tickets/{ticket_id}/reprocess
```

Der Lauf erhält `trigger=manual_recheck` und `Force=true`. Er löscht keine Historie und verändert `state-index.json` nicht rückwirkend. Im Livebetrieb gelten dennoch die normalen Auto-Schalter; für sichere Tests `DRY_RUN=true` verwenden.

## 7.3 Konfigurationsänderungen

ENV-Werte werden nur beim Start geladen. Nach Änderungen ist ein Neustart erforderlich. Anschließend `/api/status` auf die effektiven, nicht geheimen Werte prüfen. Ungültige boolesche, numerische oder Dauerwerte können von den Parserhilfen still auf den Code-Default zurückfallen; deshalb nie allein auf den Inhalt der `.env` vertrauen.

# 8. Persistenz, Backup, Reset und Wiederherstellung

## 8.1 Wichtige Dateien

| Pfad unter `DATA_DIR` | Inhalt | Bedeutung beim Löschen |
|---|---|---|
| `runs.jsonl` | vollständige Auditläufe | Diagnosehistorie verschwindet; Deduplizierung bleibt bestehen |
| `state-index.json` | letzte verarbeitete Ticketversionen und erfolgreiche Eskalationsschlüssel | Tickets gelten erneut als unbekannt; Liveaktionen können erneut geprüft werden |
| `knowledge-index/snapshot.gob` | persistenter Knowledge-Index | nächster Start muss Index neu laden/aufbauen |
| `knowledge-index/external-embeddings.json` | externer Embeddingcache | zusätzliche Embeddingarbeit |
| `embeddings.json` | historischer/zusätzlicher Embeddingcache | zusätzliche Embeddingarbeit |
| `glpi-kb-cache.json` | letzter GLPI-KB-Stand | kein Cache-Fallback bis zum nächsten erfolgreichen Sync |
| `knowledge-managed/` | über Web verwaltete Artikel | verwaltete Artikel gehen verloren |
| `category-learning.json` | menschlich bestätigte Lernbeispiele | Lernhistorie geht verloren |
| `knowledge-category-map.json` oder konfigurierter Mappingpfad | Fremdkategorie-Mapping | Kategorien werden je Modus unscoped/skip/strict behandelt |

`runs.jsonl` wird ab etwa 64 MiB auf die im Speicher gehaltenen letzten 2000 Läufe kompaktiert. `state-index.json` bleibt davon unabhängig.

## 8.2 Backup

Vor Updates oder Live-Aktivierung:

1. Agent stoppen.
2. Gesamtes `DATA_DIR` sichern.
3. `.env` separat und verschlüsselt sichern.
4. Statisches `KNOWLEDGE_DIR` und gegebenenfalls Git-Stand sichern.
5. Prüfsumme oder Snapshot-Zeitpunkt dokumentieren.

## 8.3 Sicherer Testreset

Für ein einzelnes Ticket: manuelle Neuanalyse verwenden.

Für einen vollständigen Testreset:

1. Agent stoppen.
2. `DRY_RUN=true` sicherstellen.
3. `state-index.json` sichern und löschen.
4. Optional `runs.jsonl` löschen, wenn auch die sichtbare Historie leer sein soll.
5. Agent starten.

Im Livebetrieb `state-index.json` nicht pauschal löschen. Bereits ausgeführte Kategorie-, Antwort-, Prioritäts- oder Eskalationsentscheidungen können sonst erneut geprüft werden.

## 8.4 Rollback

- Alte Binary/Image-Version wiederherstellen.
- Datenverzeichnis grundsätzlich beibehalten.
- Bei inkompatiblem Knowledge-Snapshot den Snapshot sichern und `KNOWLEDGE_INDEX_MODE=rebuild` nutzen.
- `state-index.json` nicht durch eine ältere, unvollständige Kopie ersetzen, wenn seitdem Live-Eskalationen gelaufen sind.

# 9. Diagnose, Endpunkte und Monitoring

## 9.1 HTTP-Endpunkte

| Methode/Pfad | Auth | Zweck |
|---|---|---|
| `GET /healthz` | nein | Prozess lebt; liefert einfach `status=ok` |
| `GET /readyz` | nein | 200 nur wenn GLPI, Ollama und Knowledge bereit sind |
| `GET /metrics` | nein | Prometheus-Metriken |
| `GET /` | Basic Auth, außer anonym | Dashboard |
| `GET /diagnostics` | Basic Auth | Entscheidungsdiagnose |
| `GET /category-mappings` | Basic Auth | Kategorie-Mapping-Editor |
| `GET /api/status` | Basic Auth | effektive nicht geheime Konfiguration und Laufzustand |
| `GET /api/runs?limit=50` | Basic Auth | letzte Runs, maximal 200 |
| `GET /api/diagnostics/run/{id}` | Basic Auth | einzelner Ticketlauf |
| `GET /api/diagnostics/analysis/{id}` | Basic Auth | einzelner AnalysisRun |
| `GET/POST/PUT/DELETE /api/knowledge…` | Basic Auth; Mutation zusätzlich Editfreigabe | Knowledge-Verwaltung |
| `GET/POST/DELETE /api/learning…` | Basic Auth; Mutation | Lernbeispiele |
| `POST /api/tickets/{id}/reprocess` | Basic Auth; Mutation | manuelle erzwungene Neuanalyse |
| `POST /webhook/glpi` | Webhook-Secret | Ticket in Webhook-Queue stellen |

## 9.2 Prometheus-Metriken

- `glpi_agent_processed_total`
- `glpi_agent_skipped_total`
- `glpi_agent_errors_total`
- `glpi_agent_category_changes_total`
- `glpi_agent_replies_total`
- `glpi_agent_priority_recommendations_total`
- `glpi_agent_priority_changes_total`
- `glpi_agent_escalation_runs_total`
- `glpi_agent_escalations_total`
- `glpi_agent_context_fetches_total`
- `glpi_agent_context_errors_total`
- `glpi_agent_queue_depth`
- `glpi_agent_glpi_up`
- `glpi_agent_ollama_up`
- `glpi_agent_knowledge_documents`
- `glpi_agent_glpi_kb_up`
- `glpi_agent_glpi_kb_documents`
- `glpi_agent_ollama_node_healthy{node="…"}`
- `glpi_agent_ollama_node_available{node="…"}`
- `glpi_agent_ollama_node_inflight{node="…"}`
- `glpi_agent_ollama_node_requests_total{node="…"}`
- `glpi_agent_ollama_node_failures_total{node="…"}`
- `glpi_agent_ollama_node_average_duration_ms{node="…"}`

## 9.3 Loginterpretation

Der Agent schreibt strukturierte JSON-Logs nach stdout. Wichtige Startmeldungen:

- `web server started`
- `knowledge initialization started in background`
- `persistent knowledge index loaded` oder Aufbaufortschritt
- `GLPI knowledge base synchronized`
- `ticket processing started`
- `initial GLPI ticket poll completed`
- `Ollama pool configured`
- `Ollama node available` beziehungsweise `Ollama node unavailable`

Der initiale Poll zeigt `fetched`, `already_processed`, `unseen`, `enqueued` und `rejected`. Damit lässt sich unterscheiden, ob GLPI keine Tickets liefert, alle Versionen bereits bekannt sind oder die Queue blockiert.

# 10. Eskalation im Detail

## 10.1 Kandidatenauswahl

Der Scheduler startet sofort und danach alle `ESCALATION_SCAN_INTERVAL`. Er nutzt `GLPI_ESCALATION_FILTER`; ist dieser leer, wird `GLPI_TICKET_FILTER` verwendet. Tickets werden nach Erstellungszeit ausgewählt und erst ab `ESCALATION_MIN_AGE` in die Queue gestellt.

## 10.2 Deterministische Evidenz

Vor dem Modell werden berechnet:

- Ticketalter;
- letzte menschliche Aktivität und Inaktivitätsdauer;
- keine Zuweisung (`AssignedGroups` und `AssignedUsers` leer);
- SLA-Frist aus `time_to_resolve`;
- SLA verletzt oder innerhalb des Risikofensters;
- relevantester Major Incident oberhalb des Schwellwertes.

Followups des `GLPI_AGENT_USER_ID` zählen nicht als menschliche Aktivität. Jeder andere Followup zählt derzeit als menschlich, auch eine Rückmeldung des Antragstellers.

## 10.3 Reason Codes

| Code | Datenbezug/Wirkung |
|---|---|
| `no_human_response` | muss durch Inaktivitätsberechnung belegt sein |
| `unassigned` | muss durch leere Gruppen- und Benutzerzuweisung belegt sein |
| `sla_at_risk` | muss durch Frist innerhalb `ESCALATION_SLA_RISK_WINDOW` belegt sein |
| `sla_breached` | muss durch überschrittene `time_to_resolve` belegt sein |
| `major_incident_candidate` | muss durch relevanten Major-Incident-Kontext belegt sein |
| `security_incident_suspected` | fachlicher Modellgrund; Voraussetzung für Security-Zuweisung |
| `business_deadline` | fachlicher Modellgrund, kann Second-Level unterstützen |
| `no_workaround` | fachlicher Modellgrund, kann Second-Level unterstützen |

Alle ausgegebenen Codes müssen in `ESCALATION_ALLOWED_REASON_CODES` stehen. Für die deterministisch prüfbaren Codes blockiert eine fehlende Evidenz fail-closed.

## 10.4 Aktionen und Reihenfolge

Das Modell darf höchstens drei Aktionen empfehlen. Die Policy dedupliziert und sortiert sie fest:

1. `assign_security_team`
2. `link_major_incident`
3. `assign_second_level`
4. `raise_priority`
5. `notify_service_owner`
6. `request_manager_review`

Nur die tatsächlich empfohlenen Aktionen werden ausgeführt; die Reihenfolge verhindert, dass das Modell die Ausführungskette manipuliert.

## 10.5 Teilweise erfolgreiche Pläne

Jeder Aktionsschritt besitzt einen eigenen Auditdatensatz. Eine Aktion kann erfolgreich sein, während eine andere fehlschlägt. Erfolgreiche Schritte erhalten sofort ihren dauerhaften Idempotenzschlüssel. Fehlgeschlagene Schritte können in einem späteren Lauf erneut versucht werden.

Private Notizfehler werden als Warnung am Schritt erfasst; die Hauptaktion kann trotzdem als ausgeführt gelten. Ein Webhookfehler bei Service Owner oder Manager gilt dagegen als Aktionsfehler.

## 10.6 Webhook

Der ausgehende Webhook sendet JSON mit Ticket-ID, Entity, Priorität, Stufe, Aktion, Ziel, Reason Codes, Begründung, Confidence und Idempotenzschlüssel. Derselbe Schlüssel steht im Header `Idempotency-Key`. Redirects werden nicht verfolgt. Optional wird `Authorization: Bearer …` gesetzt.

# 11. Priorisierung im Detail

## 11.1 Modelloutput

- `recommended_priority`: 1–6
- `recommended_impact`: 1–6
- `recommended_urgency`: 1–6
- `affected_scope`: `single_user`, `multiple_users`, `site`, `organization`, `unknown`
- `time_criticality`: `low`, `normal`, `high`, `immediate`, `unknown`
- kontrollierte Reason Codes
- Confidence und Begründung

## 11.2 Erhöhungsgründe

Standardmäßig freigegeben:

- `multiple_users_affected`
- `site_affected`
- `organization_affected`
- `core_service_unavailable`
- `security_incident_suspected`
- `data_loss_possible`
- `legal_or_regulatory_risk`
- `business_deadline`
- `no_workaround`
- `safety_relevant`
- `exam_or_event_critical`

Neutrale Codes wie `single_user_affected`, `workaround_available` und `insufficient_information` dürfen eine unveränderte Empfehlung erklären, aber keine automatische Erhöhung begründen.

## 11.3 Fail-open-Eigenschaft

`PRIORITY_ANALYSIS_TIMEOUT` begrenzt nur den optionalen Prioritätslauf. Timeout, ungültiges JSON oder Modellfehler führen zu `priority_ai_failed`, nicht zum Abbruch der Kategorie- und Antwortpipeline.

# 12. Knowledge/RAG und automatische Antworten

## 12.1 Source-Trennung

- `KNOWLEDGE_ALLOWED_SOURCES`: normale Suche und Antwortkandidaten.
- `KNOWLEDGE_CATEGORY_SOURCES`: nur Kategorieunterstützung; Text/HTML nicht als Antwort nutzbar.
- `KNOWLEDGE_AUTO_REPLY_SOURCES`: Teilmenge der normalen Quellen, die grundsätzlich antworten darf.

## 12.2 Indexmodi

- `incremental`: Snapshot sofort laden, Änderungen im Hintergrund einarbeiten.
- `rebuild`: Quellen vollständig neu prüfen und Index neu schreiben.
- `readonly`: ausschließlich kompatiblen Snapshot verwenden; ohne Snapshot Startfehler der Knowledge-Initialisierung.

Ticketpolling und Worker starten erst nach einem konsistenten lokalen Knowledge-Index. Das Webinterface startet vorher und zeigt den Fortschritt.

## 12.3 Kategoriekompatibilität

- `unscoped`: Artikel bleibt nutzbar; unbekannte String-Kategorien blockieren nicht automatisch.
- `skip`: Artikel mit nicht gemappten Kategorien wird ausgelassen.
- `strict`: nicht gemappte Kategorie erzeugt einen Fehler.

Für gemeinsam genutzte Knowledge-Verzeichnisse ist `unscoped` der kompatibelste Startwert; für streng kontrollierte Auto-Replies ist ein vollständiges Mapping vorzuziehen.

---

# 13. Vollständige ENV-Referenz

## 13.1 Allgemeine Syntaxregeln

- **Boolean:** empfohlen ausschließlich `true` oder `false`.
- **Dauer:** Go-Syntax wie `250ms`, `30s`, `5m`, `2h`, `72h`. `1d` ist ungültig; `24h` verwenden.
- **Score/Confidence:** Dezimalpunkt, z. B. `0.88`.
- **Listen:** kommasepariert. Stringlisten erkennen häufig `none` als leere Liste.
- **Templates:** literales `\n` wird bei `envTemplate` in einen Zeilenumbruch umgewandelt.
- **Geheimnisse:** niemals in Diagnoseexporte, Tickets oder Screenshots aufnehmen.
- **Code-Default:** Wert, wenn die Variable nicht gesetzt oder bei vielen Parsern syntaktisch ungültig ist.
- **Beispielwert:** Wert aus der mitgelieferten `.env.example`; er ist nicht automatisch eine sichere Produktionsempfehlung.

## 00. DEPLOYMENT / IMAGE – CONTAINER REGISTRY
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `AGENT_IMAGE` | Compose/optionale KB-App | OCI-Image des Agenten für das Registry-Deployment. | OCI-Image: registry/repository:tag oder registry/repository@sha256:… | nicht vom Agenten gelesen | gitea.example.de/organisation/glpi-ai-agent:latest | Nur docker-compose.registry.yml. |

## 01. DOCKER COMPOSE - PORTS
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `AGENT_PORT` | Compose/optionale KB-App | Veröffentlichter Host-Port des Agent-Dashboards in einer übergeordneten Stack-Konfiguration. | TCP-Port 1–65535; in den aktuellen Compose-Dateien nicht automatisch verwendet. | nicht vom Agenten gelesen | 7080 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KB_SEARCH_PORT` | Compose/optionale KB-App | Host-Port der optionalen Knowledge-Suche. | TCP-Port 1–65535. | nicht vom Agenten gelesen | 7081 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KB_EDITOR_PORT` | Compose/optionale KB-App | Host-Port der optionalen Knowledge-Administration. | TCP-Port 1–65535. | nicht vom Agenten gelesen | 7082 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 02. DOCKER COMPOSE - GEMEINSAME DATENVERZEICHNISSE
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `KB_DATA_PATH` | Compose/optionale KB-App | Gemeinsam gemountetes Knowledge-Verzeichnis auf dem Host. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | ./knowledge | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KB_BACKUP_PATH` | Compose/optionale KB-App | Backup-Verzeichnis der optionalen KB-Verwaltung. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | ./backups | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KB_STAGING_PATH` | Compose/optionale KB-App | Staging-Verzeichnis für neu erzeugte oder noch nicht freigegebene KB-Inhalte. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | ./staging | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 03. KNOWLEDGE-BASE WEBANWENDUNGEN – KB EDITOR
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `EDITOR_TITLE` | Compose/optionale KB-App | Titel der optionalen KB-Editor-Oberfläche. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | KB Administration | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `EDITOR_SUBTITLE` | Compose/optionale KB-App | Untertitel der optionalen KB-Editor-Oberfläche. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | Wissensbasis verwalten | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `EDITOR_AUTH_USER` | Compose/optionale KB-App | Basic-Auth-Benutzer der optionalen KB-Editor-Oberfläche. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | leer | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `EDITOR_AUTH_PASSWORD` | Compose/optionale KB-App | Basic-Auth-Passwort der optionalen KB-Editor-Oberfläche. | Geheimer Textwert; nicht in Logs, Tickets oder Screenshots veröffentlichen. | nicht vom Agenten gelesen | <SECRET> | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 03. KNOWLEDGE-BASE WEBANWENDUNGEN – KB SEARCH
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `SEARCH_TITLE` | Compose/optionale KB-App | Titel der optionalen KB-Suche. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | Stadt Hilden - KB-Datenbank | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `SEARCH_SUBTITLE` | Compose/optionale KB-App | Untertitel der optionalen KB-Suche. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | Interne Lösungsdatenbank | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `SEARCH_AUTH_USER` | Compose/optionale KB-App | Basic-Auth-Benutzer der optionalen KB-Suche. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | leer | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `SEARCH_AUTH_PASSWORD` | Compose/optionale KB-App | Basic-Auth-Passwort der optionalen KB-Suche. | Geheimer Textwert; nicht in Logs, Tickets oder Screenshots veröffentlichen. | nicht vom Agenten gelesen | <SECRET> | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `AUTO_RELOAD_INTERVAL` | Compose/optionale KB-App | Intervall, in dem die Suchanwendung die KB-Dateien erneut einliest. 30s 60s 5m | Go-Dauer, z. B. 250ms, 30s, 5m, 2h, 72h. Kein Suffix d; 24h statt 1d verwenden. | nicht vom Agenten gelesen | 60s | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 04. KNOWLEDGE-BASE WEBANWENDUNGEN - OLLAMA
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `AI_FALLBACK_ENABLED` | Compose/optionale KB-App | Aktiviert KI-Fallback in den optionalen KB-Webanwendungen, nicht im Agenten. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `OLLAMA_BASE_URL` | Compose/optionale KB-App | Ollama-URL der optionalen KB-Webanwendungen. | Absolute URL; vorzugsweise HTTPS, sofern nicht ausdrücklich lokaler Dienst. | nicht vom Agenten gelesen | http://ollama:11434 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `OLLAMA_MODEL` | Agent + optionale KB-App | Chat-Modell. Diese Variable wird aktuell sowohl von den KB-Anwendungen als auch vom Agenten verwendet. Dadurch verwenden alle Anwendungen dasselbe Modell. | Freier Text beziehungsweise installationsspezifischer Wert. | qwen3:8b | qwen3:8b | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `OLLAMA_TIMEOUT` | Agent + optionale KB-App | Gemeinsamer Timeout. | Go-Dauer, z. B. 250ms, 30s, 5m, 2h, 72h. Kein Suffix d; 24h statt 1d verwenden. | 10m | 10m | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `OLLAMA_MAX_CONCURRENT` | Agent + optionale KB-App | Rückwärtskompatibler Parallelitätswert. Im Agenten dient er nur als Fallback für `OLLAMA_NODE_MAX_INFLIGHT`, wenn die neue Variable nicht gesetzt ist. | Ganzzahl 1–32. | 1 | 1 | Für neue Pool-Installationen `OLLAMA_NODE_MAX_INFLIGHT` verwenden. |
| `OLLAMA_STAGING_AUTO_REPLY` | Compose/optionale KB-App | Legt fest, ob von KB-Webanwendungen erzeugte Staging-Artikel auto_reply=true erhalten. | Freier Text beziehungsweise installationsspezifischer Wert. | nicht vom Agenten gelesen | false | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `OLLAMA_STAGING_MIN_SCORE` | Compose/optionale KB-App | min_score für von KB-Webanwendungen erzeugte Staging-Artikel. | Dezimalzahl; bei Scores typischerweise 0.0–1.0. | nicht vom Agenten gelesen | 0.70 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 05. GLPI AI AGENT - ALLGEMEINER BETRIEB
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `DRY_RUN` | Agent | Der Agent analysiert vollständig, schreibt aber keine Änderungen nach GLPI. Durch die Policy freigegebene Aktionen werden tatsächlich ausgeführt. Für Tests / Einführung: true | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `LOG_LEVEL` | Agent | debug info warn error | debug \| info \| warn \| error | info | info | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `HTTP_ADDR` | Agent | HTTP-Listener INNERHALB des Agent-Containers. AGENT_PORT oben bestimmt dagegen den veröffentlichten Host-Port. | Go-Listenadresse, z. B. :7080, 127.0.0.1:7080 oder 0.0.0.0:7080. | :8080 | :7080 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `DATA_DIR` | Agent | Persistentes Verzeichnis IM Container. Compose mountet: agent-data:/app/data Enthält unter anderem: - Knowledge-Index - Audit/Run-Daten - Category Learning - Managed Knowledge - GLPI-KB-Cache | Freier Text beziehungsweise installationsspezifischer Wert. | ./data | /app/data | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 06. AGENT WEBUI / API / DIAGNOSE
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `WEB_USERNAME` | Agent | Benutzer für Agent-Dashboard, Knowledge-Verwaltung und Diagnose-Cockpit. | Freier Text beziehungsweise installationsspezifischer Wert. | leer | admin | Pflicht, wenn WEB_ALLOW_ANONYMOUS=false. |
| `WEB_PASSWORD` | Agent | Web Password. | Mindestens 12 Zeichen; darf keinen CHANGE_ME-Platzhalter enthalten. | leer | <SECRET> | Pflicht, wenn WEB_ALLOW_ANONYMOUS=false. |
| `WEB_ALLOW_ANONYMOUS` | Agent | Anmeldung erforderlich. Weboberfläche ohne Authentifizierung erreichbar. In Produktion normalerweise false. | true \| false | false | false | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `AI_CONTENT_LABEL_ENABLED` | Agent (derzeit ohne ENV-Bindung) | TrustedNet-Kennzeichnung vor automatisch ausgewählten Antworten. TrustedNet-KI-Badge wird vor Anrede und Antwort eingefügt. keine KI-Kennzeichnung. | true \| false; siehe Hinweis zur aktuellen Build-Abweichung. | effektiv false (Build-Abweichung) | true | Im aktuellen Quellstand nicht durch config.Load eingelesen; siehe bekannte Abweichungen. |

## 07. OPTIONALER GLPI-WEBHOOK
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `WEBHOOK_SECRET` | Agent | Optionales Shared Secret für eingehende GLPI-Webhooks. Der Absender muss dasselbe Secret z. B. über: X-Webhook-Secret übertragen. Leer lassen, falls kein Webhook verwendet wird. | Leer = eingehender Webhook deaktiviert; gesetzt mindestens 24 Zeichen und kein CHANGE_ME-Platzhalter. | leer | <SECRET> | Leer deaktiviert POST /webhook/glpi vollständig. |

## 08. GLPI 11 / HIGH-LEVEL API / OAUTH2
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `GLPI_URL` | Agent | Glpi Url. | Absolute URL; vorzugsweise HTTPS, sofern nicht ausdrücklich lokaler Dienst. | leer | https://glpi.example.com | Pflicht. |
| `GLPI_API_VERSION` | Agent | Verwendete GLPI High-Level API. | API-Versionssegment, im Projekt für v2.3 ausgelegt. | v2.3 | v2.3 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_CLIENT_ID` | Agent | OAuth2 Service Account. | Geheimer Textwert; nicht in Logs, Tickets oder Screenshots veröffentlichen. | leer | <SECRET> | Pflicht. |
| `GLPI_CLIENT_SECRET` | Agent | Glpi Client Secret. | Geheimer Textwert; nicht in Logs, Tickets oder Screenshots veröffentlichen. | leer | <SECRET> | Pflicht. |
| `GLPI_USERNAME` | Agent | Glpi Username. | Freier Text beziehungsweise installationsspezifischer Wert. | leer | ai | Pflicht. |
| `GLPI_PASSWORD` | Agent | Glpi Password. | Geheimer Textwert; nicht in Logs, Tickets oder Screenshots veröffentlichen. | leer | <SECRET> | Pflicht. |
| `GLPI_AGENT_USER_ID` | Agent | Numerische GLPI-Benutzer-ID des Service-Accounts. Wird unter anderem benötigt, um Agent-Followups von menschlichen Followups unterscheiden zu können. | Positive numerische GLPI-Benutzer-ID; 0 = nicht gesetzt. | 0 | 999 | Pflicht bei AUTO_REPLY=true und AUTO_ESCALATION=true; auch im Shadow Mode zur Aktivitätserkennung empfohlen. |
| `GLPI_ALLOW_INSECURE_HTTP` | Agent | Nur für lokale Testsysteme ohne TLS. Produktion: false | true \| false; true nur für isolierte Tests. | false | false | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 09. GLPI TICKET-POLLING
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `GLPI_ALLOWED_STATUS_IDS` | Agent | Fail-closed Whitelist erlaubter GLPI-Ticketstatus. 1 1,2 Status 1 entspricht typischerweise "Neu". | Kommagetrennte positive Status-IDs, z. B. 1 oder 1,2. | nicht ermittelt | 1 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_POLL_INTERVAL` | Agent | Polling-Intervall. | Go-Dauer, z. B. 250ms, 30s, 5m, 2h, 72h. Kein Suffix d; 24h statt 1d verwenden. | 30s | 30s | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_POLL_LIMIT` | Agent | Maximale Anzahl Tickets pro Poll. | Positive Ganzzahl; praktisch passend zur Ticketmenge und API-Latenz wählen. | 50 | 50 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_TICKET_FILTER` | Agent | Optionale serverseitige Vorfilterung. Die Agent-Policy prüft GLPI_ALLOWED_STATUS_IDS anschließend trotzdem selbst. Änderungen der Syntax immer gegen /api.php/doc der eigenen GLPI-Instanz prüfen. | GLPI-High-Level-API-Filterausdruck; Syntax gegen /api.php/doc prüfen. | leer | status.id==1 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_TIMEOUT` | Agent | HTTP-Timeout für GLPI-Aufrufe. | Go-Dauer, z. B. 250ms, 30s, 5m, 2h, 72h. Kein Suffix d; 24h statt 1d verwenden. | 20s | 20s | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 10. GLPI AI AGENT - OLLAMA
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `OLLAMA_URL` | Agent | Rückwärtskompatible Einzelnode-Adresse. Wird nur genutzt, wenn `OLLAMA_URLS` leer ist. | Absolute HTTP-/HTTPS-URL ohne Zugangsdaten. | http://ollama:11434 | http://ollama:11434 | Optional; bei leerem `OLLAMA_URLS` wirksam. |
| `OLLAMA_URLS` | Agent | Kommagetrennte Liste aller Ollama-Nodes. Jeder Node führt vollständige Inferenzrequests aus. | 1..64 absolute HTTP-/HTTPS-URLs, z. B. `http://10.0.0.21:11434,http://10.0.0.22:11434`. Keine Duplikate. | leer; effektiver Fallback auf `OLLAMA_URL` | leer | Für Poolbetrieb erforderlich. |
| `OLLAMA_NODE_NAMES` | Agent | Lesbare, positionsgleiche Namen für Dashboard, Metriken und AnalysisRun-Diagnose. | Kommagetrennte eindeutige, nicht leere Namen; Anzahl exakt wie `OLLAMA_URLS`. Leer = automatisch aus Hostname. | leer | leer | Optional. |
| `OLLAMA_NODE_WEIGHTS` | Agent | Positionsgleiche Leistungsgewichte für `weighted`. Höhere Werte erhalten anteilig mehr Requests. | Kommagetrennte Ganzzahlen 1–100; Anzahl exakt wie `OLLAMA_URLS`. Leer = Gewicht 1 je Node. | leer / effektiv 1 | leer | Nur für `OLLAMA_ROUTING_MODE=weighted`. |
| `OLLAMA_NODE_MAX_INFLIGHT` | Agent | Maximale gleichzeitig laufende Requests **je Node**. | Ganzzahl 1–32. Für integrierte GPUs zunächst 1. | 0 in Parser; effektiver Fallback auf `OLLAMA_MAX_CONCURRENT` = 1 | 1 | Zentraler Ressourcen-Schutz je Node. |
| `OLLAMA_ROUTING_MODE` | Agent | Auswahlstrategie für einen verfügbaren Node. | `least_inflight` \| `round_robin` \| `weighted` \| `fastest_recent` | least_inflight | least_inflight | `least_inflight` für gleichartige Nodes empfohlen. |
| `OLLAMA_NODE_HEALTH_INTERVAL` | Agent | Intervall der `/api/tags`-Prüfung auf Erreichbarkeit, Modelle und Digests. | Go-Dauer >= 1s. | 15s | 15s | Optional. |
| `OLLAMA_NODE_FAILURE_COOLDOWN` | Agent | Sperrzeit nach retryfähigem Requestfehler, um flappende Nodes vorübergehend nicht neu zu belasten. | Go-Dauer >= 0; 0 deaktiviert Cooldown. | 30s | 30s | Optional. |
| `OLLAMA_NODE_REQUEST_TIMEOUT` | Agent | Maximale Dauer eines einzelnen HTTP-Versuchs an genau einen Node. Ein kürzerer Analyse-Kontext-Timeout hat Vorrang. | Go-Dauer > 0. | 0 im Parser; effektiver Fallback auf `OLLAMA_TIMEOUT` = 10m | 10m | Optional. |
| `OLLAMA_FAILOVER_ENABLED` | Agent | Wiederholt einen noch nicht akzeptierten Inferenzrequest bei retryfähigem Fehler auf einem anderen kompatiblen Node. | true \| false | true | true | Kein GLPI-Write findet innerhalb des Failovers statt. |
| `OLLAMA_FAILOVER_ATTEMPTS` | Agent | Maximale Zahl verschiedener Nodes pro HTTP-Request. | 0 = automatisch alle Nodes; sonst Ganzzahl 1 bis Nodeanzahl. | 0 / effektiv Nodeanzahl | 0 | Nur bei aktiviertem Failover. |
| `OLLAMA_REQUIRE_SAME_MODEL_DIGEST` | Agent | Verlangt identische Chat- und erforderliche Embedding-Modelldigests. Bei Abweichung arbeitet der Pool vollständig fail-closed. | true \| false | true | true | Für reproduzierbare Entscheidungen empfohlen. |
| `OLLAMA_REQUIRE_EMBEDDING_MODEL` | Agent | Verlangt das konfigurierte Embedding-Modell auf jedem Node. Bei false dürfen Chat-only-Nodes teilnehmen; Embedding-Requests werden weiterhin nur an Nodes mit Embeddingmodell gesendet. | true \| false | true | true | Bei `RAG_ENABLED=true` empfohlen. |
| `OLLAMA_EMBEDDING_MODEL` | Agent | OLLAMA_MODEL ist bereits oben im gemeinsamen Compose-/Ollama-Bereich gesetzt: OLLAMA_MODEL=qwen3:8b Embedding-Modell für RAG. | Freier Text beziehungsweise installationsspezifischer Wert. | embeddinggemma | embeddinggemma | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_EMBEDDING_PROFILE` | Agent | Modellspezifisches Retrieval-Prompting. auto Modell automatisch erkennen und passende Retrieval-Prompts verwenden. Für embeddinggemma empfohlen. plain keine modellspezifischen Retrieval-Prompts. | auto \| plain \| embeddinggemma | auto | auto | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `OLLAMA_NUM_PREDICT` | Agent | OLLAMA_TIMEOUT und OLLAMA_MAX_CONCURRENT sind bereits oben gesetzt. Maximale Anzahl generierter Tokens für strukturierte Antworten. | Ganzzahl 1–4096. | 768 | 768 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `OLLAMA_JSON_RETRIES` | Agent | Wiederholungen bei fehlerhaftem / abgeschnittenem JSON. | Ganzzahl 0–3. | 1 | 1 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `OLLAMA_KEEP_ALIVE` | Agent | Ollama-Modell nach Benutzung im Speicher halten. 5m 10m 30m | Dauer >= 0; 0 ist zulässig. | 10m | 10m | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `OLLAMA_THINK` | Agent | Thinking bei unterstützten Modellen deaktivieren. Für strukturierte Klassifikations-/Policy-Aufgaben empfohlen. | true \| false | false | false | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 11. KNOWLEDGE BASE / RAG - BASIS
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `KNOWLEDGE_DIR` | Agent | Knowledge-Verzeichnis IM Agent-Container. Compose sollte hierhin KB_DATA_PATH mounten: ${KB_DATA_PATH:-./knowledge}:/app/knowledge:ro | Freier Text beziehungsweise installationsspezifischer Wert. | ./knowledge | /app/knowledge | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `RAG_ENABLED` | Agent | Gesamtes Retrieval-System aktivieren. | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 12. EXTERNE KNOWLEDGE-KATEGORIEN
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `KNOWLEDGE_CATEGORY_MODE` | Agent | Verhalten bei String-/Fremdkategorien, z. B.: "AI-Staging" "Outlook" "E-Mail" "Signatur" unscoped Artikel bleibt nutzbar. Fremdkategorien können als Retrieval-Metadaten dienen. skip Artikel mit unbekannten Kategorien überspringen. strict unbekannte Kategorie als Fehler behandeln. Für eine gemeinsam mit anderen Anwendungen verwendete KB: unscoped | unscoped \| skip \| strict | unscoped | unscoped | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_CATEGORY_MAP_FILE` | Agent | Optionales Mapping von Fremdkategorien auf GLPI-ITIL-Kategorie-IDs. Beispiel knowledge-category-map.json: { "Outlook": 12, "E-Mail": 12, "Active Directory": 2, "Security": [20,21] } | Freier Text beziehungsweise installationsspezifischer Wert. | leer | /app/data/knowledge-category-map.json | Für den Mapping-Editor zusätzlich KNOWLEDGE_WEB_EDIT_ENABLED=true erforderlich. |
| `KNOWLEDGE_IGNORE_GLOBS` | Agent | Optional bestimmte KB-Dateien ignorieren. KB-SEC-ATTCK-*.json legacy-*.json,external-only-*.json keine zusätzlichen Ignore-Regeln. | Kommagetrennte filepath.Match-Globs; Groß-/Kleinschreibung bleibt erhalten. | leer | leer | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 13. PERSISTENTER KNOWLEDGE-INDEX
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `KNOWLEDGE_INDEX_MODE` | Agent | incremental Persistent gespeicherten Index sofort verwenden. Neue/geänderte Dateien anschließend inkrementell nachziehen. Für Produktion empfohlen. rebuild vollständigen Index neu erzeugen. readonly nur bestehenden Index verwenden, keine Änderungen übernehmen. | incremental \| rebuild \| readonly | incremental | incremental | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_EMBED_BATCH_SIZE` | Agent | Anzahl Texte pro Embedding-Batch. | 0 oder 1–256. | 64 | 64 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_INDEX_SCAN_INTERVAL` | Agent | Intervall für neue/geänderte/gelöschte Dateien. 30s 1m 5m keinen automatischen Hintergrundscan durchführen. | Dauer >= 0; 0 deaktiviert Hintergrundscans. | 5m | 5m | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 14. RETRIEVAL / DYNAMISCHE KANDIDATENAUSWAHL
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `KNOWLEDGE_RETRIEVAL_FLOOR` | Agent | Unterhalb dieses Retrieval-Scores wird eine KB nicht als geeigneter Kandidat betrachtet. Der Wert ist KEINE Wahrscheinlichkeit. | 0.0–1.0. | 0.30 | 0.30 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 14. RETRIEVAL / DYNAMISCHE KANDIDATENAUSWAHL – MAX_GAP               0.20
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `KNOWLEDGE_CANDIDATE_MAX_GAP` | Agent | dynamischer Cutoff 0.62 Ein Kandidat mit 0.55 würde dann nicht an die KI gesendet. | 0.0–1.0. | 0.20 | 0.20 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_TOP_K` | Agent | Maximale Anzahl Knowledge-Kandidaten, die tatsächlich an Ollama gehen. | 0 oder 1–20; 0 führt im Ticketpfad zum internen Fallback 6. | 6 | 6 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_AUDIT_TOP_K` | Agent | Anzahl Kandidaten für Audit / Diagnose. Kann größer als KNOWLEDGE_TOP_K sein. | 0 oder mindestens KNOWLEDGE_TOP_K und höchstens 50. | 10 | 10 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 15. HYBRID-RETRIEVAL - RANKING-GEWICHTE
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `KNOWLEDGE_WEIGHT_SEMANTIC` | Agent | Die Werte beschreiben die Gewichtung beim KB-Ranking. Summe aktuell: 1.0 Fehlende Metadaten sollen nicht automatisch negativ bewertet werden. Embedding-/Chunk-Semantik. | 0.0–1.0. | 0.45 | 0.45 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_WEIGHT_TITLE` | Agent | Ticket-Betreff gegenüber KB-Titel. | 0.0–1.0. | 0.20 | 0.20 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_WEIGHT_LEXICAL` | Agent | Lexikalische / sprachliche Übereinstimmung. | 0.0–1.0. | 0.20 | 0.20 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_WEIGHT_KEYWORDS` | Agent | KB-Keywords. | 0.0–1.0. | 0.075 | 0.075 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_WEIGHT_CATEGORY` | Agent | Kategorie-/Lernsignal. | 0.0–1.0. | 0.075 | 0.075 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 16. FINALE EVIDENZ FÜR AUTO-REPLY
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `KNOWLEDGE_MIN_SCORE` | Agent | Mindestwert der FINALEN Evidenz. WICHTIG: Das ist nicht der reine Retrieval-Score. Die finale Evidenz kombiniert: - Retrieval - AI Confidence - Kategorieübereinstimmung | 0.0–1.0. | 0.70 | 0.70 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_EVIDENCE_WEIGHT_RETRIEVAL` | Agent | Gewicht Retrieval. | >= 0; die Evidenzberechnung normalisiert durch die Summe aktiver Gewichte. | 0.45 | 0.45 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_EVIDENCE_WEIGHT_AI` | Agent | Gewicht KI-Auswahl / KI-Confidence. | >= 0; die Evidenzberechnung normalisiert durch die Summe aktiver Gewichte. | 0.35 | 0.35 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_EVIDENCE_WEIGHT_CATEGORY` | Agent | Gewicht Kategorieübereinstimmung. | >= 0; die Evidenzberechnung normalisiert durch die Summe aktiver Gewichte. | 0.20 | 0.20 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 17. KNOWLEDGE-CHUNKING
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `KNOWLEDGE_CHUNK_WORDS` | Agent | Ungefähre Anzahl Wörter pro Dokument-Chunk. | 0 oder 40–1000. | 160 | 160 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_CHUNK_OVERLAP_WORDS` | Agent | Überlappung benachbarter Chunks. | >= 0 und kleiner als KNOWLEDGE_CHUNK_WORDS. | 30 | 30 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_MAX_CHUNKS_PER_DOC` | Agent | Maximale Anzahl Chunks pro KB-Dokument. | 0 oder 1–100. | 24 | 24 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_MAX_QUERY_CHUNKS` | Agent | Maximale Anzahl Query-Chunks bei sehr langen Tickets. | 0 oder 1–200. | 64 | 64 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CATEGORY_PROMPT_LIMIT` | Agent | Maximale Anzahl Kategorien im Kategorie-Prompt. | Ganzzahl; 0 bedeutet je nach Variable deaktiviert/nicht gesetzt oder interner Fallback. | 80 | 80 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 18. KNOWLEDGE-QUELLEN / TRUST POLICY
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `KNOWLEDGE_ALLOWED_SOURCES` | Agent | Quellen für normale Knowledge-Suche und mögliche Antwortkandidaten. Indexiert wird die Vereinigung mit KNOWLEDGE_CATEGORY_SOURCES. internal-kb glpi-kb runbook vendor-docs | Kommagetrennte, kleingeschriebene Source-Namen; mindestens ein Wert. | internal-kb | internal-kb,glpi-kb,vendor-docs,vendor-docs-ms,vendor-docs-linux,vendor-docs-sec | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_CATEGORY_SOURCES` | Agent | Quellen, die ausschließlich die Kategorieentscheidung unterstützen. Ohne explizite Angabe wird aus Kompatibilitätsgründen KNOWLEDGE_ALLOWED_SOURCES verwendet. Mit "none" wird Knowledge-Einfluss auf die Kategorisierung deaktiviert. | Kommagetrennte Source-Namen; none = keine Kategorie-KB. Nicht gesetzt = Rückfall auf KNOWLEDGE_ALLOWED_SOURCES. | leer | internal-category | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_AUTO_REPLY_SOURCES` | Agent | Nur diese Quellen dürfen grundsätzlich automatische Antworten liefern. Muss eine Teilmenge von KNOWLEDGE_ALLOWED_SOURCES sein. Beispiel zum kompletten Abschalten: KNOWLEDGE_AUTO_REPLY_SOURCES=none | Kommagetrennte Teilmenge von KNOWLEDGE_ALLOWED_SOURCES; none = keine Knowledge-Quelle für Auto-Reply. | internal-kb | internal-kb,glpi-kb,vendor-docs,vendor-docs-ms,vendor-docs-linux,vendor-docs-sec | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `KNOWLEDGE_WEB_EDIT_ENABLED` | Agent | Webbasierte Bearbeitung von Agent-eigenen Knowledge-Artikeln. Diese werden unter: DATA_DIR/knowledge-managed gespeichert. Das statische KNOWLEDGE_DIR bleibt read-only. | true \| false | false | true | Erfordert WEB_ALLOW_ANONYMOUS=false. |

## 19. GLPI KNOWLEDGE BASE CONNECTOR
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `GLPI_KB_ENABLED` | Agent | GLPI-interne Knowledge Base synchronisieren. | true \| false | false | true | Aktiviert periodische Synchronisierung; Quelle muss in der Index-Source-Union enthalten sein. |
| `GLPI_KB_PATH` | Agent | Agent ermittelt die KnowbaseItem-Route aus /api.php/doc.json. | auto oder absoluter API-Pfad beginnend mit /. | auto | auto | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_KB_FILTER` | Agent | Optionaler serverseitiger GLPI-Filter. alle für den Service Account sichtbaren Artikel, begrenzt durch LIMIT. | Freier Text beziehungsweise installationsspezifischer Wert. | leer | leer | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_KB_LIMIT` | Agent | Maximale Anzahl GLPI-KB-Artikel. | 1–5000. | 500 | 500 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_KB_SYNC_INTERVAL` | Agent | Synchronisationsintervall. | Dauer >= 1m. | 10m | 10m | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_KB_SOURCE` | Agent | source-Wert importierter GLPI-KB-Artikel. | Freier Text beziehungsweise installationsspezifischer Wert. | glpi-kb | glpi-kb | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_KB_AUTO_REPLY` | Agent | GLPI-KB-Artikel können grundsätzlich Auto-Replies auslösen. Zusätzlich gelten weiterhin alle anderen Policy-Gates. | true \| false | false | true | Bei true: GLPI_KB_SOURCE muss in normalen und Auto-Reply-Quellen stehen; Kategorie-ID-Whitelist darf nicht leer sein. |
| `GLPI_KB_AUTO_REPLY_CATEGORY_IDS` | Agent | Whitelist der GLPI KNOWLEDGE-BASE-Kategorie-IDs. WICHTIG: Dies sind NICHT die ITIL-/Ticketkategorie-IDs. Mehrere Werte: 1,2,7 | Kommagetrennte positive GLPI-KB-Kategorie-IDs; leer/none = keine. | nicht ermittelt | 1 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 20. HUMAN-IN-THE-LOOP / KATEGORIE-LERNEN
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `LEARNING_ENABLED` | Agent | Menschlich bestätigte/korrigierte Entscheidungen als Lernbeispiele verwenden. Der Agent lernt NICHT automatisch aus seinen eigenen unbestätigten Entscheidungen. | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `LEARNING_MAX_EXAMPLES` | Agent | Maximale Anzahl gespeicherter Beispiele. | Bei aktiviertem Lernen 1–10000. | 500 | 500 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `LEARNING_EXAMPLES_PER_CATEGORY` | Agent | Maximale Beispiele pro Kategorie im Prompt. | Bei aktiviertem Lernen 1–20. | 5 | 5 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 21. KOMMUNIKATIONSPOLICY
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `COMMUNICATION_LANGUAGE` | Agent | Erwartete Sprache von Auto-Reply-KBs. | Freier Text beziehungsweise installationsspezifischer Wert. | de-DE | de-DE | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `COMMUNICATION_STYLE` | Agent | Erwarteter Kommunikationsstil. | formal \| neutral \| informal | formal | formal | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `COMMUNICATION_SALUTATION` | Agent | Wird vor die Knowledge-Antwort gesetzt. | Freier Text beziehungsweise installationsspezifischer Wert. | Guten Tag, | Guten Tag, | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `COMMUNICATION_CLOSING` | Agent | Abschluss. | Freier Text beziehungsweise installationsspezifischer Wert. | Mit freundlichen Grüßen | Mit freundlichen Grüßen | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `COMMUNICATION_SIGNATURE` | Agent | Communication Signature. | Freier Text beziehungsweise installationsspezifischer Wert. | IT-Service | IT-Service | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 22. OPERATIONAL CONTEXT - GLOBAL
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `CONTEXT_ENABLED` | Agent | Globaler Schalter für zusätzliche Betriebsinformationen: - Changes - Major Incidents - Requester-Geräte - Uptime Kuma | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CONTEXT_TIMEOUT` | Agent | Timeout für Kontextabfragen. | Go-Dauer, z. B. 250ms, 30s, 5m, 2h, 72h. Kein Suffix d; 24h statt 1d verwenden. | 12s | 12s | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CONTEXT_RELEVANCE_MIN_SCORE` | Agent | Mindestscore, ab dem Incident/Outage als für das Ticket relevant gilt. | 0.0–1.0. | 0.20 | 0.20 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CONTEXT_BLOCK_AUTO_REPLY_ON_ERRORS` | Agent | Fehler einer aktivierten Kontextquelle blockieren Auto-Reply. Fail-closed und für Produktion empfohlen. | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CONTEXT_BLOCK_AUTO_REPLY_ON_INCIDENT` | Agent | relevante zentrale Störung blockiert individuelle Standardantwort. | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 23. GLPI CHANGE CALENDAR
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `CHANGE_CALENDAR_ENABLED` | Agent | Change Calendar Enabled. | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_CHANGE_PATH` | Agent | API-Route. | Absoluter API-Pfad, z. B. /Assistance/Change. | /Assistance/Change | /Assistance/Change | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_CHANGE_FILTER` | Agent | Optionaler serverseitiger GLPI-Filter. | Freier Text beziehungsweise installationsspezifischer Wert. | leer | leer | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_CHANGE_LIMIT` | Agent | Maximale Anzahl geladener Changes. | 1–1000. | 100 | 100 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CHANGE_LOOKBACK` | Agent | Betrachteter Zeitraum in der Vergangenheit. | Go-Dauer, z. B. 250ms, 30s, 5m, 2h, 72h. Kein Suffix d; 24h statt 1d verwenden. | 48h | 72h | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CHANGE_LOOKAHEAD` | Agent | Betrachteter Zeitraum in der Zukunft. | Go-Dauer, z. B. 250ms, 30s, 5m, 2h, 72h. Kein Suffix d; 24h statt 1d verwenden. | 24h | 24h | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 24. MAJOR INCIDENTS
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `MAJOR_INCIDENTS_ENABLED` | Agent | Major Incidents über GLPI-Tickets ermitteln. Erst aktivieren, wenn GLPI_MAJOR_INCIDENT_FILTER getestet wurde. | true \| false | false | false | Bei true ist GLPI_MAJOR_INCIDENT_FILTER Pflicht. |
| `GLPI_MAJOR_INCIDENT_FILTER` | Agent | Expliziter Filter für Tickets, die als Major Incident gelten. | Freier Text beziehungsweise installationsspezifischer Wert. | leer | leer | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_MAJOR_INCIDENT_LIMIT` | Agent | Glpi Major Incident Limit. | 1–500. | 20 | 20 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 25. REQUESTER -> GERÄT / ASSET CONTEXT
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `USER_DEVICE_CONTEXT_ENABLED` | Agent | Zusätzlich zu direkt verknüpften Ticket-Assets Geräte des Requesters suchen. | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_USER_DEVICE_PATHS` | Agent | Asset-Routen. | Kommagetrennte absolute API-Pfade. | /Assets/Computer | /Assets/Computer | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_USER_DEVICE_FILTER_TEMPLATE` | Agent | {{user_id}} wird vom Agenten ersetzt. | Filtertext mit zwingendem Platzhalter {{user_id}}. | user.id=={{user_id}} | user.id=={{user_id}} | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_USER_DEVICE_LIMIT` | Agent | Maximale Anzahl Geräte je Suche. | 1–500. | 20 | 20 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 26. UPTIME KUMA
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `UPTIME_KUMA_ENABLED` | Agent | Globaler Schalter für Uptime-Kuma-Kontext. | true \| false | false | false | Bei true: URL Pflicht; metrics benötigt API-Key, status_page benötigt Slugs. |
| `UPTIME_KUMA_URL` | Agent | Uptime Kuma Url. | Absolute URL; vorzugsweise HTTPS, sofern nicht ausdrücklich lokaler Dienst. | leer | https://uptime.example.com | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `UPTIME_KUMA_MODE` | Agent | metrics authentifizierte Prometheus-Metrics. status_page öffentliche/publizierte Statusseiten. | metrics \| status_page | metrics | metrics | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `UPTIME_KUMA_API_KEY` | Agent | Nur in metrics erforderlich. | Geheimer Textwert; nicht in Logs, Tickets oder Screenshots veröffentlichen. | leer | <SECRET> | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `UPTIME_KUMA_STATUS_PAGES` | Agent | Nur in status_page erforderlich. Mehrere Slugs: it-services,network,applications | Kommagetrennte Liste; Leerzeichen werden an den Rändern entfernt. | leer | it-services | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `UPTIME_KUMA_TIMEOUT` | Agent | Uptime Kuma Timeout. | Go-Dauer, z. B. 250ms, 30s, 5m, 2h, 72h. Kein Suffix d; 24h statt 1d verwenden. | 10s | 10s | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `UPTIME_KUMA_MAX_ISSUES` | Agent | Maximale Anzahl gleichzeitig berücksichtigter Probleme. | 1–200. | 20 | 20 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `UPTIME_KUMA_INCLUDE_MAINTENANCE` | Agent | Maintenance ebenfalls als Kontext berücksichtigen. | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CONTEXT_STATUS_REPLY_ENABLED` | Agent | Optional: bei eindeutig passender Uptime-Kuma-Störung oder Wartung einen ausschließlich vom Betreiber vorgegebenen Text senden. Die KI erzeugt keinen Antworttext; sie wählt nur einen aktiven Kandidaten und liefert eine Confidence. | true \| false | false | false | Erfordert CONTEXT_ENABLED=true, UPTIME_KUMA_ENABLED=true und beide vordefinierten Textvorlagen. |
| `CONTEXT_STATUS_REPLY_MIN_RELEVANCE` | Agent | Context Status Reply Min Relevance. | 0.0–1.0. | 0.50 | 0.50 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CONTEXT_STATUS_REPLY_MIN_AI_CONFIDENCE` | Agent | Context Status Reply Min Ai Confidence. | 0.0–1.0. | 0.80 | 0.80 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CONTEXT_STATUS_REPLY_MIN_FINAL_SCORE` | Agent | Finaler Score = Relevanz × KI-Confidence. | 0.0–1.0. | 0.45 | 0.45 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CONTEXT_INCIDENT_REPLY_TEXT` | Agent | Literal \n wird als Zeilenumbruch interpretiert. Verfügbare Platzhalter: {{service_name}}, {{status}}, {{status_page}}, {{message}}, {{incident_title}}, {{incident_content}}, {{last_heartbeat}} | Textvorlage; literales \n wird zu einem Zeilenumbruch. Nur dokumentierte Platzhalter verwenden. | leer | Zu Ihrer Meldung liegt derzeit wahrscheinlich eine zentrale Störung bei {{service_name}} vor. Die Einschränkung kann damit zusammenhängen. Wir beobachten den Status. | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `CONTEXT_MAINTENANCE_REPLY_TEXT` | Agent | Context Maintenance Reply Text. | Textvorlage; literales \n wird zu einem Zeilenumbruch. Nur dokumentierte Platzhalter verwenden. | leer | Für {{service_name}} läuft derzeit eine Wartung. Die von Ihnen beschriebene Einschränkung kann damit zusammenhängen. Bitte testen Sie den Dienst nach Abschluss der Wartung erneut. | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 27. POLICY-GATES
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `AUTO_CATEGORY` | Agent | Automatische Kategorisierung zulassen. | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `AUTO_REPLY` | Agent | Automatische Antworten grundsätzlich zulassen. DRY_RUN=true verhindert trotzdem das tatsächliche Schreiben nach GLPI. | true \| false | false | true | true erfordert GLPI_AGENT_USER_ID und mindestens eine Auto-Reply-Quelle. |
| `CATEGORY_CONFIDENCE` | Agent | Mindestconfidence der KI für Kategorieänderungen. | 0.0–1.0. | 0.90 | 0.90 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `REPLY_CONFIDENCE` | Agent | Mindestconfidence der KI für Antwortauswahl. Dies allein reicht NICHT für Auto-Reply. Zusätzlich gelten unter anderem: - Knowledge-Evidenz - Retrieval-Regeln - Source Policy - KB auto_reply - Kommunikationspolicy - Followup-Prüfung - Kontext-/Incident-Regeln - zweite Followup-Prüfung unmittelbar vor dem Schreiben | 0.0–1.0. | 0.97 | 0.97 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 28. KI-PRIORISIERUNG
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `PRIORITY_ENABLED` | Agent | Separater KI-Lauf zur Empfehlung der GLPI-Priorität. Der Lauf wird im Diagnose-Cockpit unabhängig von Kategorie, Status und Antwort gespeichert. | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `AUTO_PRIORITY` | Agent | Standardmäßig Shadow Mode: Empfehlung und Policy-Gates werden protokolliert, GLPI wird nicht verändert. Für Live-Schreibzugriffe zusätzlich DRY_RUN=false. | true \| false | false | false | true erfordert PRIORITY_ENABLED=true; tatsächlicher Write zusätzlich DRY_RUN=false. |
| `PRIORITY_CONFIDENCE` | Agent | Priority Confidence. | 0.0–1.0. | 0.88 | 0.88 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `PRIORITY_ANALYSIS_TIMEOUT` | Agent | Eigener Fail-open-Timeout für diesen optionalen KI-Lauf. Kategorie und Antwort laufen danach weiter. | Dauer >= 0; 0 = kein eigener Stufen-Timeout. | 45s | 45s | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `PRIORITY_MAX_INCREASE` | Agent | Automatische Erhöhung je Ticketlauf; Herabstufungen sind grundsätzlich gesperrt. | 0–5; bei AUTO_PRIORITY=true mindestens 1. | 1 | 1 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `PRIORITY_ALLOWED_REASON_CODES` | Agent | Nur kontrollierte, kommaseparierte Grundcodes dürfen eine Empfehlung tragen. | Kommagetrennte Reason Codes; bei PRIORITY_ENABLED=true mindestens einer. | multiple_users_affected,site_affected,organization_affected,core_service_unavailable,security_incident_suspected,data_loss_possible,legal_or_regulatory_risk,business_deadline,no_workaround,safety_relevant,exam_or_event_critical | multiple_users_affected,site_affected,organization_affected,core_service_unavailable,security_incident_suspected,data_loss_possible,legal_or_regulatory_risk,business_deadline,no_workaround,safety_relevant,exam_or_event_critical | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 29. ZEITGESTEUERTE KI-ESKALATION
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `ESCALATION_ENABLED` | Agent | Unabhängiger Scheduler. Er prüft offene Tickets auch ohne Änderung von date_mod. | true \| false | false | false | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `AUTO_ESCALATION` | Agent | Standardmäßig werden nur Diagnose-/Shadow-Läufe erzeugt. Live-Ausführung benötigt zusätzlich DRY_RUN=false und GLPI_AGENT_USER_ID. | true \| false | false | false | true erfordert ESCALATION_ENABLED=true, mindestens eine ausführbare Aktion, Zielkonfiguration und DRY_RUN=false für Writes. |
| `ESCALATION_SCAN_INTERVAL` | Agent | Escalation Scan Interval. | Dauer >= 1m. | 15m | 15m | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_MIN_AGE` | Agent | Mindestalter des Tickets seit date_creation, bevor es in den Eskalationsscan gelangt. | Dauer >= 1m. | 4h | 4h | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_MIN_INACTIVITY` | Agent | Mindestdauer seit der letzten menschlichen Aktivität für den Grund no_human_response. SLA-, Security- und Major-Incident-Gründe können unabhängig davon greifen. Agent-Followups werden über GLPI_AGENT_USER_ID ausgenommen. | 0 oder Dauer >= 1m; 0 verwendet ESCALATION_MIN_AGE. | 2h | 2h | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_ANALYSIS_TIMEOUT` | Agent | Eigenes KI-Zeitbudget; blockiert die normalen Ticketläufe nicht unbegrenzt. | Dauer >= 0; 0 = kein eigener Stufen-Timeout. | 45s | 45s | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_CONFIDENCE` | Agent | Escalation Confidence. | 0.0–1.0. | 0.88 | 0.88 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_MAX_LEVEL` | Agent | Escalation Max Level. | 1–4. | 3 | 3 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_SLA_RISK_WINDOW` | Agent | Zeitfenster vor time_to_resolve, in dem sla_at_risk deterministisch wahr wird. | Dauer >= 0; 0 deaktiviert sla_at_risk. | 2h | 2h | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_SERVICE_OWNER_MIN_LEVEL` | Agent | Aktionsspezifische Mindeststufen. | 0 oder 1–4; 0 ergibt Laufzeit-Fallback Stufe 2. | 2 | 2 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_MANAGER_REVIEW_MIN_LEVEL` | Agent | Escalation Manager Review Min Level. | 0 oder 1–4; 0 ergibt Laufzeit-Fallback Stufe 3. | 3 | 3 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_MAJOR_INCIDENT_MIN_RELEVANCE` | Agent | Mindest-Relevanz eines vom Kontextkollektor gelieferten Major Incidents. | 0.0–1.0. | 0.50 | 0.50 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_ALLOWED_REASON_CODES` | Agent | Escalation Allowed Reason Codes. | Kommagetrennte kontrollierte Eskalationsgründe; mindestens einer bei aktivierter Eskalation. | no_human_response,sla_at_risk,sla_breached,business_deadline,no_workaround,security_incident_suspected,unassigned,major_incident_candidate | no_human_response,sla_at_risk,sla_breached,business_deadline,no_workaround,security_incident_suspected,unassigned,major_incident_candidate | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_ALLOWED_ACTIONS` | Agent | Jede Aktion muss einzeln freigegeben werden. Sichere Einführung: zunächst nur none,raise_priority; weitere Aktionen erst nach Konfiguration der Ziele aktivieren. Verfügbar: none,raise_priority,assign_second_level,assign_security_team, notify_service_owner,link_major_incident,request_manager_review | none \| raise_priority \| assign_second_level \| assign_security_team \| notify_service_owner \| link_major_incident \| request_manager_review; kommasepariert. | none,raise_priority | none,raise_priority | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_SECOND_LEVEL_GROUP_ID` | Agent | Zielgruppen/-benutzer für Zuweisungs- und Benachrichtigungsaktionen. Es handelt sich um numerische GLPI-IDs. | Numerische GLPI-Gruppen-ID; 0 = nicht konfiguriert. | 0 | 0 | Pflicht im Livebetrieb, wenn assign_second_level freigegeben ist. |
| `ESCALATION_SECURITY_GROUP_ID` | Agent | Escalation Security Group Id. | Numerische GLPI-Gruppen-ID; 0 = nicht konfiguriert. | 0 | 0 | Pflicht im Livebetrieb, wenn assign_security_team freigegeben ist. |
| `ESCALATION_SERVICE_OWNER_GROUP_ID` | Agent | Escalation Service Owner Group Id. | Numerische GLPI-Gruppen-ID; 0 = nicht konfiguriert. | 0 | 0 | Mindestens Gruppe, Benutzer oder Webhook für notify_service_owner. |
| `ESCALATION_SERVICE_OWNER_USER_ID` | Agent | Escalation Service Owner User Id. | Numerische GLPI-Benutzer-ID; 0 = nicht konfiguriert. | 0 | 0 | Mindestens Gruppe, Benutzer oder Webhook für notify_service_owner. |
| `ESCALATION_MANAGER_REVIEW_GROUP_ID` | Agent | Escalation Manager Review Group Id. | Numerische GLPI-Gruppen-ID; 0 = nicht konfiguriert. | 0 | 0 | Mindestens Gruppe, Benutzer oder Webhook für request_manager_review. |
| `ESCALATION_MANAGER_REVIEW_USER_ID` | Agent | Escalation Manager Review User Id. | Numerische GLPI-Benutzer-ID; 0 = nicht konfiguriert. | 0 | 0 | Mindestens Gruppe, Benutzer oder Webhook für request_manager_review. |
| `ESCALATION_ADD_PRIVATE_FOLLOWUP` | Agent | Zu jeder ausgeführten Aktion kann ein privater GLPI-Followup geschrieben werden. | true \| false | true | true | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_SECOND_LEVEL_NOTE` | Agent | Escalation Second Level Note. | Textvorlage; literales \n wird zu einem Zeilenumbruch. Nur dokumentierte Platzhalter verwenden. | Automatische Eskalation Stufe {{level}}: Übergabe an den Second-Level-Support. Gründe: {{reason_codes}}. KI-Begründung: {{reason}} | Automatische Eskalation Stufe {{level}}: Übergabe an den Second-Level-Support. Gründe: {{reason_codes}}. KI-Begründung: {{reason}} | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_SECURITY_NOTE` | Agent | Escalation Security Note. | Textvorlage; literales \n wird zu einem Zeilenumbruch. Nur dokumentierte Platzhalter verwenden. | Automatische Eskalation Stufe {{level}}: Übergabe an das Security-Team. Gründe: {{reason_codes}}. KI-Begründung: {{reason}} | Automatische Eskalation Stufe {{level}}: Übergabe an das Security-Team. Gründe: {{reason_codes}}. KI-Begründung: {{reason}} | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_SERVICE_OWNER_NOTE` | Agent | Escalation Service Owner Note. | Textvorlage; literales \n wird zu einem Zeilenumbruch. Nur dokumentierte Platzhalter verwenden. | Automatische Eskalation Stufe {{level}}: Service Owner wurde zur Prüfung einbezogen. Gründe: {{reason_codes}}. KI-Begründung: {{reason}} | Automatische Eskalation Stufe {{level}}: Service Owner wurde zur Prüfung einbezogen. Gründe: {{reason_codes}}. KI-Begründung: {{reason}} | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_MAJOR_INCIDENT_NOTE` | Agent | Escalation Major Incident Note. | Textvorlage; literales \n wird zu einem Zeilenumbruch. Nur dokumentierte Platzhalter verwenden. | Automatische Eskalation Stufe {{level}}: Verknüpfung mit Major Incident #{{major_incident_id}} ({{major_incident_name}}). Relevanz: {{major_incident_score}}. Gründe: {{reason_codes}}. | Automatische Eskalation Stufe {{level}}: Verknüpfung mit Major Incident #{{major_incident_id}} ({{major_incident_name}}). Relevanz: {{major_incident_score}}. Gründe: {{reason_codes}}. | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_MANAGER_REVIEW_NOTE` | Agent | Escalation Manager Review Note. | Textvorlage; literales \n wird zu einem Zeilenumbruch. Nur dokumentierte Platzhalter verwenden. | Automatische Eskalation Stufe {{level}}: Management-Review angefordert. Gründe: {{reason_codes}}. KI-Begründung: {{reason}} | Automatische Eskalation Stufe {{level}}: Management-Review angefordert. Gründe: {{reason_codes}}. KI-Begründung: {{reason}} | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_WEBHOOK_URL` | Agent | Optionaler ausgehender Webhook für Service-Owner- und Management-Benachrichtigungen. Das Token wird nie über die Status-API ausgegeben. | Absolute http(s)-URL; HTTP nur mit ESCALATION_WEBHOOK_ALLOW_INSECURE_HTTP=true. | leer | leer | Optional; Ziel für Service-Owner-/Management-Benachrichtigungen. |
| `ESCALATION_WEBHOOK_BEARER_TOKEN` | Agent | Escalation Webhook Bearer Token. | Geheimer Textwert; nicht in Logs, Tickets oder Screenshots veröffentlichen. | leer | leer | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_WEBHOOK_TIMEOUT` | Agent | Escalation Webhook Timeout. | Dauer > 0, wenn eine URL gesetzt ist. | 10s | 10s | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `ESCALATION_WEBHOOK_ALLOW_INSECURE_HTTP` | Agent | Nur für isolierte Testnetze; HTTPS ist der sichere Standard. | true \| false; true nur für isolierte Tests. | false | false | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_ESCALATION_GROUP_PATCH_FIELD` | Agent | GLPI-Adapter für Zuweisungen. Die Feldnamen müssen zur OpenAPI-Beschreibung der konkreten GLPI-Installation passen. Unterstützte Payload-Formen: assigned_groups/assigned_users = Liste von {"id":...}; group/group_tech/user/user_tech = einzelnes {"id":...}. | Einfacher JSON-Feldname aus Buchstaben, Ziffern und Unterstrich. | assigned_groups | assigned_groups | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_ESCALATION_USER_PATCH_FIELD` | Agent | Glpi Escalation User Patch Field. | Einfacher JSON-Feldname aus Buchstaben, Ziffern und Unterstrich. | assigned_users | assigned_users | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_ESCALATION_ITIL_LINK_PATH` | Agent | Installationsspezifischer Adapter für link_major_incident. Beide Werte sind erforderlich. Platzhalter im Pfad/JSON: {{ticket_id}}, {{source_ticket_id}}, {{major_incident_id}}, {{target_ticket_id}}. | Absoluter API-Pfad ohne Query/Fragment, mit Ticket-/Major-Incident-Platzhaltern. | leer | leer | Gemeinsam mit GLPI_ESCALATION_ITIL_LINK_BODY; Pflicht für live link_major_incident. |
| `GLPI_ESCALATION_ITIL_LINK_BODY` | Agent | Glpi Escalation Itil Link Body. | Gültiges JSON nach Platzhalterersetzung; muss Quell- und Ziel-ID referenzieren. | leer | leer | Gemeinsam mit GLPI_ESCALATION_ITIL_LINK_PATH; Pflicht für live link_major_incident. |
| `GLPI_ESCALATION_FILTER` | Agent | Leer = GLPI_TICKET_FILTER verwenden. Für Produktion ausdrücklich auf offene, eskalierbare Status und die gewünschte Einheit beschränken. | GLPI-Filter; leer = GLPI_TICKET_FILTER. | leer | leer | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `GLPI_ESCALATION_LIMIT` | Agent | Glpi Escalation Limit. | 1–1000. | 100 | 100 | Optional; Wirkung abhängig von aktivierten Funktionen. |

## 30. WORKER / PRIORITÄTSQUEUE
| ENV | Geltungsbereich | Bedeutung und Auswirkung | Mögliche Werte / Format | Code-Default | `.env.example` | Pflicht / Abhängigkeiten |
|---|---|---|---|---|---|---|
| `QUEUE_SIZE` | Agent | Maximale Anzahl wartender Jobs. | Ganzzahl >= 1. | 256 | 256 | Optional; Wirkung abhängig von aktivierten Funktionen. |
| `WORKERS` | Agent | Parallele Ticket-Worker. Darf größer als die Gesamtzahl gleichzeitig verfügbarer Node-Slots sein. Ollama wird je Node durch OLLAMA_NODE_MAX_INFLIGHT begrenzt. | Ganzzahl >= 1. | 2 | 2 | Optional; Wirkung abhängig von aktivierten Funktionen. |

> **Vollständigkeitskontrolle:** In dieser Referenz sind 178 Variablen beschrieben, einschließlich `AGENT_IMAGE` aus dem Registry-Compose und aller 177 Zuweisungen aus `.env.example`.


# 14. Fehlerbehebung

## 14.1 Keine Tickets werden verarbeitet

1. Dashboard-Pollhinweis lesen.
2. `fetched=0`: GLPI-Filter, Rechte und API prüfen.
3. `fetched>0`, `unseen=0`: alle Treffer stehen in `state-index.json`; neues/geändertes Ticket oder manuelle Neuanalyse verwenden.
4. `unseen>0`, `enqueued=0`, `rejected>0`: Queue voll oder Trigger bereits pending.
5. `enqueued>0`, aber kein Run: Worker, Ollama-Limit und Logs prüfen.
6. `knowledge_ready=false`: erster Indexaufbau läuft oder ist fehlgeschlagen; Ticketverarbeitung wartet.

## 14.2 Knowledge bleibt nicht bereit

- `KNOWLEDGE_DIR` existiert und ist lesbar?
- `DATA_DIR` schreibbar?
- Embeddingmodell vorhanden?
- `KNOWLEDGE_INDEX_MODE=readonly` ohne Snapshot?
- Ungültiges JSON, Source nicht erlaubt oder `strict`-Kategoriefehler?
- `/api/status` Felder `knowledge_init_error` und `knowledge_last_scan_error` prüfen.

## 14.3 Agent startet nicht

Häufige Konfigurationsfehler:

- fehlende GLPI-Pflichtvariablen;
- Webpasswort unter 12 Zeichen;
- HTTP-GLPI ohne ausdrückliche Testfreigabe;
- Auto-Reply ohne Agent-Benutzer-ID;
- Auto-Priority ohne Priority-Analyse;
- Auto-Escalation ohne Aktion/Ziel;
- Major Incidents ohne Filter;
- Uptime Kuma im falschen Modus ohne Key/Slug;
- Statusreply ohne Templates;
- ungültiger ITIL-Linkadapter.

## 14.4 Auto-Reply wird nicht geschrieben

In der Diagnose die blockierenden Gates prüfen: vorhandener Followup, KI-Ablehnung, Confidence, Source, `auto_reply`, Sprache, Stil, Retrieval-Floor, finale Evidenz, Kategorie-Scope, Kontextfehler, relevanter Incident oder Ticketänderung vor Write.

## 14.5 Eskalationsaktion bleibt im Shadow Mode

Ein Schritt ist nur live, wenn gleichzeitig gilt:

```env
ESCALATION_ENABLED=true
AUTO_ESCALATION=true
DRY_RUN=false
```

Zusätzlich müssen Aktion, Ziel, Mindeststufe, Reason Codes, Evidenz, Confidence und Idempotenz passen.

## 14.6 Port nicht erreichbar

Listener `HTTP_ADDR` und Container-Mapping müssen denselben Containerport verwenden. Bei nativem Betrieb Firewall und Bind-Adresse prüfen. `127.0.0.1` erlaubt nur lokalen Zugriff; `:7080` bindet alle Interfaces.

## 14.7 Ollama-Pool hat keine verfügbaren Nodes

1. `/api/status` prüfen: `ollama_nodes`, `healthy`, `compatible`, `last_error` und Digests.
2. Auf jedem Node `OLLAMA_MODEL` und `OLLAMA_EMBEDDING_MODEL` installieren.
3. Bei Digest-Abweichung die Modell-Tags auf allen Nodes erneut auf denselben Stand ziehen; nicht vorschnell `OLLAMA_REQUIRE_SAME_MODEL_DIGEST=false` setzen.
4. Firewall prüfen: Der Agent muss `/api/tags`, `/api/chat` und `/api/embed` erreichen.
5. `OLLAMA_NODE_MAX_INFLIGHT=1` verwenden und prüfen, ob Requests nur wegen voller Slots warten.
6. Nach einem Fehler `cooldown_until` beachten; der Node wird während des Cooldowns absichtlich nicht gewählt.
7. Neue AnalysisRuns unter `provider.attempts` prüfen. Dort stehen Node, HTTP-Status, Timeout, Retryfähigkeit und Failover.

## 14.8 Pool verteilt nicht wie erwartet

- `least_inflight` verteilt nach aktuell laufenden Requests, nicht streng abwechselnd. Bei seriellen Tests kann daher derselbe schnellere Node mehrfach gewählt werden.
- `round_robin` für eine sichtbar zyklische Verteilung verwenden.
- `weighted` benötigt positionsgleiche `OLLAMA_NODE_WEIGHTS`.
- `fastest_recent` bevorzugt die gemessene gleitende Durchschnittslaufzeit und kann langsame Nodes bewusst selten verwenden.
- Ein einzelner KI-Request wird nicht über mehrere Rechner beschleunigt; der Nutzen entsteht bei mehreren parallelen Tickets oder Analyseläufen.

# 15. Bekannte Grenzen und Abweichungen

1. **`AI_CONTENT_LABEL_ENABLED`:** Das Feld ist im Modell und in der Policy vorhanden, wird im vorliegenden `config.Load()` aber nicht aus der ENV geladen. Bei normalem Start bleibt der effektive Wert daher `false`, unabhängig von `.env.example`. Vor Nutzung der Kennzeichnung ist eine Codekorrektur erforderlich.
2. **Compose-Portabweichung:** `.env.example` setzt `HTTP_ADDR=:7080`; `docker-compose.yml` mappt jedoch `8080:8080`. Unverändert zusammen verwendet sind Listener und Mapping inkonsistent. `compose_local.yml` passt zu 7080.
3. **`AGENT_PORT`:** Wird in den vorliegenden Compose-Dateien nicht referenziert und ändert den Agent-Listener nicht. Maßgeblich ist `HTTP_ADDR` plus Port-Mapping.
4. **Optionale KB-Webanwendungen:** Die ENV-Blöcke für Editor/Search/Fallback gehören zu einem größeren Stack. Die aktuellen Compose-Dateien dieses Pakets starten nur Agent und Ollama; diese Variablen haben dort keine Wirkung.
5. **Followup-Erkennung:** Bei Eskalationen zählt jeder Nicht-Agent-Followup als menschliche Aktivität, auch ein Followup des Antragstellers. Eine Rollenunterscheidung ist derzeit nicht implementiert.
6. **Prioritätsfelder:** Impact und Urgency werden analysiert und auditiert, aber aktuell nicht separat nach GLPI geschrieben.
7. **Major-Incident-Link:** Pfad und Payload sind installationsspezifisch und müssen gegen die OpenAPI-Dokumentation der konkreten GLPI-Instanz getestet werden.
8. **Zuweisungsfelder:** `assigned_groups`/`assigned_users` passen nicht zwingend zu jeder GLPI-Version oder Plugin-Konfiguration. Im Shadow Mode und mit Testticket validieren.
9. **Parser-Fallback:** Ungültige Booleans, Zahlen und Dauern fallen häufig still auf den Code-Default zurück. Effektive Werte über `/api/status` kontrollieren.
10. **Audit enthält Ticketinhalte:** `runs.jsonl` speichert Input-Snapshots und kann personenbezogene oder vertrauliche Ticketdaten enthalten. Zugriffsrechte, Backup und Löschkonzept entsprechend behandeln.
11. **Keine atomare Servertransaktion:** Prewrite-Recheck reduziert Rennen, ersetzt aber keinen GLPI-seitigen Conditional Write.
12. **Eskalationsscan und Limit:** Bei sehr vielen alten Tickets und kleinem Limit können dieselben ältesten Kandidaten wiederholt zuerst erscheinen. Filter und Limit passend dimensionieren.
13. **Kein Model-Sharding:** Der Ollama-Pool bündelt weder RAM noch GPU-Speicher mehrerer Rechner. Jeder Node muss die verwendeten Modelle vollständig lokal laden können.
14. **Einzelrequest-Latenz:** Ein Request läuft vollständig auf einem Node. Mehr Nodes erhöhen Durchsatz und Ausfallsicherheit, nicht automatisch die Tokens/s eines einzelnen Requests.
15. **Ollama-Netzwerkzugriff:** Node-APIs müssen durch Firewall/VPN/Reverse-Proxy begrenzt werden; der Agent bringt keine eigene Node-Zugangsdatenverwaltung mit.

# 16. Betriebs-Checklisten

## 16.1 Vor jedem Releasewechsel

- [ ] `DATA_DIR` vollständig gesichert.
- [ ] `.env` verschlüsselt gesichert.
- [ ] Aktuelle Binary-/Image-Prüfsumme dokumentiert.
- [ ] Release zunächst mit `DRY_RUN=true` gestartet.
- [ ] `/readyz`, `/api/status` und initialer Poll geprüft.
- [ ] Knowledge-Snapshot kompatibel oder Rebuild eingeplant.
- [ ] Keine unbeabsichtigten Änderungen an `state-index.json`.

## 16.2 Vor Auto-Reply live

- [ ] `GLPI_AGENT_USER_ID` korrekt.
- [ ] Source-Whitelists minimal.
- [ ] Knowledge-Artikel fachlich freigegeben.
- [ ] `auto_reply=true` nur gezielt.
- [ ] Sprache, Stil und Kategoriebindung korrekt.
- [ ] Kontextquellen stabil.
- [ ] Mehrtägige Shadow-Auswertung abgeschlossen.

## 16.3 Vor erweiterten Eskalationsaktionen live

- [ ] Offene Status und Einheiten im `GLPI_ESCALATION_FILTER` begrenzt.
- [ ] Gruppen- und Benutzer-IDs mit Testticket geprüft.
- [ ] GLPI-Patchfelder gegen OpenAPI geprüft.
- [ ] Private Followup-Texte abgestimmt.
- [ ] Webhook mit Idempotency-Key getestet.
- [ ] Security-Aktion nur bei Security-Grund zulässig.
- [ ] Major-Incident-Adapter separat getestet.
- [ ] `state-index.json` wird gesichert und nicht manuell bereinigt.

## 16.4 Bei Störung

- [ ] `PRIORITY_ENABLED=false` setzen, wenn nur der optionale Prioritätslauf auffällig ist.
- [ ] `ESCALATION_ENABLED=false` setzen, wenn Scheduler/Aktionen auffällig sind.
- [ ] `AUTO_REPLY=false`, `AUTO_PRIORITY=false`, `AUTO_ESCALATION=false` setzen, um Writes gezielt zu stoppen.
- [ ] Im Zweifel `DRY_RUN=true` und neu starten.
- [ ] Logs, Run-ID und Analysis-ID sichern.
- [ ] Keine pauschale Löschung von `state-index.json` im Livebetrieb.

## 16.5 Vor Aktivierung eines Ollama-Pools

- [ ] Auf allen Nodes identisches Chatmodell installiert.
- [ ] Auf allen RAG-Nodes identisches Embeddingmodell installiert.
- [ ] Modelldigests im Dashboard identisch.
- [ ] Node-Port nur für den Agenten freigegeben.
- [ ] `OLLAMA_NODE_MAX_INFLIGHT=1` als Startwert.
- [ ] Failover mit absichtlich gestopptem Testnode geprüft.
- [ ] Neue AnalysisRuns zeigen `provider.selected_node` und Versuche.
- [ ] RAM, Temperatur und p95-Laufzeit unter paralleler Last beobachtet.

---

**Ende der Betriebsanleitung**
