# KB Helpdesk – Editor & Google-Modus

Ein einziges Go-/Docker-Image für zwei Rollen auf derselben JSON-Wissensbasis:

1. **Editor-Modus** – vollständiger Einzel- und Masseneditor mit Backups.
2. **Google-Modus** – moderne, schreibgeschützte interne Helpdesk-Suche mit Artikel-Viewer.
3. **Optionaler Ollama-Fallback** – nur bei 0 Treffern einen strukturierten KI-Entwurf erzeugen und getrennt im Staging ablegen.

Der Betriebsmodus wird ausschließlich über `APP_MODE` gewählt. Es ist kein zweiter Build und kein anderes Image nötig.

## Modi

### `APP_MODE=editor`

Der bekannte Administrationsmodus:

- Volltextsuche über ID, Titel, Text, Antwort, Keywords, Kategorien, Quelle und Pfad
- Filter für `auto_reply`, Sprache, Kommunikationsstil und Quelle
- Pagination für große Bestände mit 10.000+ Dateien
- Einzelbearbeitung als Formular
- vollständiger Raw-JSON-Editor
- Massenbearbeitung für Auswahl oder alle aktuellen Treffer
- Bulk-Setzen von `auto_reply`, `min_score`, `language`, `communication_style`, `source`, `source_uri`
- Keywords/Kategorien hinzufügen oder entfernen
- Suchen & Ersetzen in `title`, `text` und `answer`, optional per Regex
- Dry-Run/Vorschau vor Massenänderungen
- automatische Backups
- atomisches Schreiben per Temp-Datei + Rename
- Schutz vor extern veränderten Dateien
- integrierter **Produktiv/Staging-Umschalter** mit Staging-Zähler
- KI-Entwürfe im selben Formular oder Raw-JSON-Editor prüfen und korrigieren
- einzelne oder mehrere Staging-Entwürfe **Freigeben → Produktiv**
- einzelne oder mehrere Staging-Entwürfe sicher löschen (`staging/.trash`)
- Freigaben überschreiben niemals bestehende Produktiv-IDs oder Zieldateien

### `APP_MODE=google`

Reiner Helpdesk-/Viewer-Modus:

- große, reduzierte Suchoberfläche im Stil einer internen Suchmaschine
- Relevanzranking statt alphabetischer Trefferreihenfolge
- Gewichtung von ID/Fehlercode, Titel, Keywords, Kategorien, Problemtext und Antwort
- hervorgehobene Suchbegriffe
- Treffer-Auszüge aus Problem bzw. Lösung
- dynamische Schnellzugriffe aus den häufigsten Kategorien
- Pagination und URL-basierte Suchanfragen
- lesefreundlicher Artikel-Viewer
- Problem und Lösung visuell getrennt
- Antwort direkt in die Zwischenablage kopieren
- dauerhafter Link zu einem geöffneten Wissensartikel
- Quellenlink, sofern `source_uri` vorhanden ist
- responsive Oberfläche für Desktop, Tablet und Smartphone
- automatisches Neu-Einlesen des Dateiindex (standardmäßig alle 60 Sekunden)
- **keine Bearbeitungsoberfläche**
- **PUT-/Bulk-/Reload-Endpunkte werden serverseitig mit HTTP 403 gesperrt**
- optionaler Ollama-Fallback bei exakt 0 KB-Treffern
- KI-Ergebnisse werden als ungeprüfte JSON-Artikel in einem separaten Staging-Verzeichnis gespeichert

`viewer` und `search` werden zusätzlich als Alias für `google` akzeptiert. Für Deployments sollte aus Gründen der Eindeutigkeit `editor` oder `google` verwendet werden.

## Schnellstart

```bash
cp .env.example .env
docker compose up --build -d
```

Danach:

```text
http://localhost:8080
```

## Editor-Deployment

`.env`:

```dotenv
APP_MODE=editor
APP_TITLE=Knowledge Base Editor
APP_SUBTITLE=JSON · Massenbearbeitung · Docker

KB_DATA_PATH=../glpi-ai-agent-kb-microsoft-errorcodes-kompendium/knowledge
KB_DATA_MOUNT_MODE=rw
KB_BACKUP_PATH=./backups
KB_STAGING_PATH=./staging
KB_EDITOR_PORT=8080

BASIC_AUTH_USER=admin
BASIC_AUTH_PASSWORD=ein-langes-zufaelliges-passwort
```

Wichtig: Der Editor benötigt für das Knowledge-Verzeichnis `rw`.

## Google-/Helpdesk-Deployment

Dasselbe Image, nur andere ENV-Werte:

```dotenv
APP_MODE=google
APP_TITLE=IT Helpdesk Wissen
APP_SUBTITLE=Interne Lösungsdatenbank für Support und Service Desk
AUTO_RELOAD_INTERVAL=60s

KB_DATA_PATH=../glpi-ai-agent-kb-microsoft-errorcodes-kompendium/knowledge
KB_DATA_MOUNT_MODE=ro
KB_BACKUP_PATH=./backups
KB_EDITOR_PORT=8081

BASIC_AUTH_USER=helpdesk
BASIC_AUTH_PASSWORD=ein-langes-zufaelliges-passwort
```

Für den Google-Modus wird `KB_DATA_MOUNT_MODE=ro` empfohlen. Damit existieren zwei Schutzschichten:

1. Die Go-Anwendung stellt keine schreibende Funktion bereit und blockiert die schreibenden API-Endpunkte.
2. Docker mountet die JSON-Dateien zusätzlich read-only.

Der Backup-Pfad wird im Google-Modus nicht benutzt; er bleibt nur Teil derselben Compose-Konfiguration.

## Optionaler Ollama-Fallback mit Staging

Der KI-Fallback ist standardmäßig **aus**. Wird er im Google-Modus aktiviert, ist der Ablauf:

```text
Suchanfrage
   │
   ├─ normale KB hat Treffer ─────────────► normale Trefferliste
   │
   └─ normale KB hat 0 Treffer
            │
            ▼
      POST /api/ai/fallback
            │
            ▼
      Ollama /api/chat
      stream=false + JSON-Schema
            │
            ▼
      STAGING_DIR/*.json
            │
            ▼
      GET /api/staging/{id}
            │
            ▼
      Artikel-Viewer mit
      "AI-STAGING · UNGEPRÜFT"
```

Beispiel `.env` für den Search-Container:

```dotenv
APP_MODE=google
KB_DATA_MOUNT_MODE=ro

AI_FALLBACK_ENABLED=true
OLLAMA_BASE_URL=http://ollama:11434
OLLAMA_MODEL=dein-bereits-gepulltes-modell
OLLAMA_TIMEOUT=10m
OLLAMA_MAX_CONCURRENT=1

KB_STAGING_PATH=./staging
OLLAMA_STAGING_AUTO_REPLY=false
OLLAMA_STAGING_MIN_SCORE=0.78
```

`OLLAMA_MODEL` hat absichtlich keinen hartcodierten Standard. Bei aktiviertem Fallback muss ein auf deiner Ollama-Instanz vorhandenes Modell angegeben werden.

### Docker-Netzwerk zu Ollama

`OLLAMA_BASE_URL=http://ollama:11434` funktioniert, wenn der Search-Container den Ollama-Container im selben Docker-Netzwerk unter dem Service-/Containernamen `ollama` erreichen kann.

Wenn Ollama in einem anderen Compose-Stack läuft, verbindest du beide Stacks am einfachsten mit demselben externen Docker-Netzwerk und verwendest dort den Ollama-Service-Namen. Alternativ kann `OLLAMA_BASE_URL` auf einen anderen vom Search-Container erreichbaren Host gesetzt werden.

Der Browser spricht **nie direkt mit Ollama**. Nur das Go-Backend kennt `OLLAMA_BASE_URL`.

### Warum ein getrenntes Staging-Verzeichnis?

Das produktive `DATA_DIR` bleibt im Google-Modus read-only. KI-Ergebnisse werden ausschließlich in `STAGING_DIR` geschrieben. Der Pfad darf weder innerhalb von `DATA_DIR` liegen noch `DATA_DIR` enthalten; die Anwendung verweigert sonst den Start. Dadurch werden ungeprüfte KI-Entwürfe nicht durch den normalen Index aufgenommen.

Ein Staging-Artikel verwendet dasselbe JSON-Format wie die restliche Wissensbasis, zum Beispiel:

```json
{
  "id": "KB-AI-STAGING-20260729-120000-A1B2C3D4",
  "title": "...",
  "text": "...",
  "answer": "...",
  "auto_reply": false,
  "min_score": 0.78,
  "categories": ["AI-Staging", "Windows"],
  "keywords": ["..."],
  "source": "Ollama / modellname (AI-Staging)",
  "source_uri": "",
  "language": "de-DE",
  "communication_style": "formal"
}
```

`auto_reply` ist im Staging standardmäßig bewusst `false`. Das kann über `OLLAMA_STAGING_AUTO_REPLY=true` geändert werden, wird für ungeprüfte KI-Inhalte aber nicht empfohlen.

## Staging-Review und Freigabe im Editor

Der Editor bindet `STAGING_DIR` unabhängig davon ein, ob auf dieser Instanz der Ollama-Fallback aktiv ist. In einem Dual-Deployment teilen sich Search- und Editor-Container daher denselben Staging-Mount:

```text
kb-search
  knowledge  :ro
  staging    :rw   <- KI erzeugt Entwürfe

kb-editor
  knowledge  :rw   <- Freigaben landen hier
  staging    :rw   <- Helpdesk prüft Entwürfe
  backups    :rw
```

In der Editor-Oberfläche steht links oberhalb der Suche ein Umschalter **Produktiv / Staging** zur Verfügung. Die bestehenden Filter für Suchtext, `auto_reply`, Sprache, Stil und Quelle funktionieren auch auf den Staging-Dateien.

Ein Staging-Artikel kann ganz normal im Formular oder als Raw JSON bearbeitet und gespeichert werden. Im Staging-Modus erscheinen zusätzlich:

- **Freigeben → Produktiv** – legt eine neue JSON-Datei in `DATA_DIR` an und archiviert den geprüften Originalentwurf danach unter `STAGING_DIR/.approved`.
- **Löschen** – verschiebt den verworfenen Entwurf nach `STAGING_DIR/.trash`, statt ihn sofort unwiederbringlich zu löschen.
- **Staging-Aktionen** – Freigeben oder Löschen für eine Mehrfachauswahl.

Bei einer Freigabe wird der **aktuelle JSON-Inhalt unverändert** übernommen. Insbesondere bleibt `auto_reply` so gesetzt, wie der Reviewer ihn im Entwurf eingestellt hat. Dadurch kann ein KI-Entwurf zunächst mit `auto_reply: false` geprüft und erst bewusst auf `true` gesetzt werden.

Die Freigabe überschreibt niemals eine vorhandene Produktivdatei. Existiert bereits dieselbe `id` oder derselbe abgeleitete Dateiname, bricht die Operation mit einem Konflikt ab und der Staging-Entwurf bleibt erhalten.

Im Google-/Viewer-Modus bleiben alle Staging-Schreib-, Lösch- und Freigabe-Endpunkte serverseitig gesperrt.

### 10-Minuten-Timeout

`OLLAMA_TIMEOUT=10m` ist der Standard. Der Timeout wird im Request-Kontext und im Go-HTTP-Client durchgesetzt. Zusätzlich passt der Server seinen HTTP-`WriteTimeout` an, damit eine erlaubte 10-Minuten-Generierung nicht bereits nach dem normalen 60-Sekunden-Timeout abgebrochen wird.

Im Browser bleibt der Fetch-Request offen. Währenddessen zeigt die Oberfläche einen Laufzeitzähler und einen Staging-Status. Nach erfolgreicher Generierung lädt der Browser den gespeicherten Artikel erneut über die Staging-API und öffnet ihn automatisch.

### Schutz vor Missbrauch

Der KI-Endpunkt ist kein freier Chat-Proxy. Das Backend:

- akzeptiert nur eine Suchanfrage,
- begrenzt deren Länge,
- prüft unmittelbar vor Ollama erneut, dass die produktive KB wirklich `0` Treffer hat,
- begrenzt parallele Generierungen über `OLLAMA_MAX_CONCURRENT`,
- fordert von Ollama Structured Output nach einem festen JSON-Schema,
- setzt kritische Metadaten wie ID, Sprache, Quelle, `auto_reply` und `min_score` serverseitig,
- speichert atomar über Temp-Datei + Rename,
- lässt Ollama keine angeblichen Quellen/URLs in diese Metadaten schreiben.

## Ein Image, zwei Container

Als fertiges Beispiel liegt `docker-compose.dual.yml` bei. Es startet denselben Build gleichzeitig als Editor auf Port 8080 und als read-only Helpdesk-Suche auf Port 8081:

```bash
docker compose -f docker-compose.dual.yml up --build -d
```

Alternativ kann ein gebautes Image manuell zweimal gestartet werden:

```bash
docker build -t kb-helpdesk:local .
```

Editor:

```bash
docker run -d \
  --name kb-editor \
  -p 8080:8080 \
  -e APP_MODE=editor \
  -e APP_TITLE="KB Administration" \
  -v /srv/kb/knowledge:/data/knowledge:rw \
  -v /srv/kb/backups:/data/backups:rw \
  -v /srv/kb/staging:/data/staging:rw \
  kb-helpdesk:local
```

Helpdesk-Suche:

```bash
docker run -d \
  --name kb-search \
  -p 8081:8080 \
  -e APP_MODE=google \
  -e APP_TITLE="IT Helpdesk Wissen" \
  -e APP_SUBTITLE="Interne Lösungsdatenbank" \
  -v /srv/kb/knowledge:/data/knowledge:ro \
  -v /srv/kb/staging:/data/staging:rw \
  kb-helpdesk:local
```

Beide Container lesen damit denselben Bestand. Im Google-Modus wird der Dateiindex standardmäßig alle 60 Sekunden automatisch neu aufgebaut, sodass Änderungen aus dem Editor ohne Container-Neustart sichtbar werden. Mit `AUTO_RELOAD_INTERVAL=0` kann das deaktiviert werden. Der Editor besitzt zusätzlich einen manuellen Reload-Button.

## Konfiguration

| Variable | Standard | Bedeutung |
|---|---|---|
| `APP_MODE` | `editor` | `editor` oder `google`; zusätzlich Aliase `viewer`/`search` |
| `APP_TITLE` | modusabhängig | Name in Browser und Kopfzeile |
| `APP_SUBTITLE` | modusabhängig | Untertitel/Helpdesk-Beschreibung |
| `AUTO_RELOAD_INTERVAL` | Google: `60s`, Editor: aus | Dateiindex regelmäßig neu aufbauen; `0`/`off` deaktiviert |
| `DATA_DIR` | `./data/knowledge` | Wurzelverzeichnis der JSON-Dateien |
| `BACKUP_DIR` | `.kb-editor-backups` neben dem Datenordner | Backup-Ziel im Editor-Modus |
| `LISTEN_ADDR` | `:8080` | HTTP Listen-Adresse |
| `BASIC_AUTH_USER` | leer | Optionaler Basic-Auth-Benutzer |
| `BASIC_AUTH_PASSWORD` | leer | Optionales Basic-Auth-Passwort |
| `KB_DATA_PATH` | `./knowledge` | Hostpfad für Docker Compose |
| `KB_DATA_MOUNT_MODE` | `rw` | `rw` für Editor, empfohlen `ro` für Google-Modus |
| `KB_BACKUP_PATH` | `./backups` | Hostpfad für Backups |
| `KB_EDITOR_PORT` | `8080` | veröffentlichter Host-Port |
| `KB_STAGING_PATH` | `./staging` | Hostpfad für ungeprüfte KI-Entwürfe |
| `STAGING_DIR` | neben `DATA_DIR` als `staging` | Staging-Pfad im Prozess/Container |
| `AI_FALLBACK_ENABLED` | `false` | Ollama-Fallback im Google-Modus aktivieren |
| `OLLAMA_BASE_URL` | `http://ollama:11434` | Vom Go-Container erreichbare Ollama-Basis-URL |
| `OLLAMA_MODEL` | leer / erforderlich wenn aktiv | Modellname auf der Ollama-Instanz |
| `OLLAMA_TIMEOUT` | `10m` | Maximale Dauer einer Ollama-Anfrage |
| `OLLAMA_MAX_CONCURRENT` | `1` | Maximale parallele KI-Generierungen, 1–16 |
| `OLLAMA_STAGING_AUTO_REPLY` | `false` | `auto_reply` für neu erzeugte Staging-Artikel |
| `OLLAMA_STAGING_MIN_SCORE` | `0.78` | `min_score` für Staging-Artikel |

## Suche und Ranking im Google-Modus

Eine Suche muss alle eingegebenen Suchbegriffe im indexierten Dokument finden. Anschließend werden die Treffer gewichtet. Besonders hoch bewertet werden:

1. exakte ID-/Fehlercode-Treffer,
2. Titel,
3. Keywords,
4. Kategorien,
5. Problem-/Erkennungstext,
6. Antwort/Lösung,
7. Quelle.

Dadurch steht beispielsweise ein Artikel mit `0x80070005` direkt in ID/Titel vor einem Artikel, der denselben Code nur beiläufig im Lösungstext erwähnt.

Die Such-URL ist teilbar:

```text
/?q=0x80070005
```

Ein geöffneter produktiver Artikel erhält zusätzlich `doc=<interner-key>`. Ein KI-Staging-Artikel verwendet stattdessen `staging=<staging-id>` und kann damit ebenfalls intern direkt verlinkt werden.

## Tastatur

Im Google-Modus fokussiert `/` von überall die Suche.

Im Editor gelten zusätzlich die bereits vorhandenen Tastaturfunktionen, unter anderem `Ctrl+S`/`Cmd+S` zum Speichern.

## JSON-Verhalten

Die Anwendung arbeitet direkt mit `.json`-Dateien. Für Suche und Navigation liegt ein Index im RAM. Unbekannte zusätzliche JSON-Felder bleiben beim Bearbeiten erhalten.

Das bekannte Schema kann beispielsweise enthalten:

```json
{
  "id": "KB-MSERR-...",
  "title": "...",
  "text": "...",
  "answer": "...",
  "auto_reply": true,
  "min_score": 0.78,
  "categories": ["Windows"],
  "keywords": ["0x80070005"],
  "source": "Microsoft Learn",
  "source_uri": "https://learn.microsoft.com/...",
  "language": "de-DE",
  "communication_style": "formal"
}
```

## Backups im Editor-Modus

Bei einem normalen Speichern entsteht ein Zeitstempelverzeichnis, zum Beispiel:

```text
backups/
└── 20260728-153012.123456789/
    └── KB-MSERR-ACT-00001.json
```

Bei einer Massenänderung werden alle Originaldateien desselben Vorgangs gemeinsam gesichert. Die relative Unterverzeichnisstruktur bleibt erhalten.

Backups werden nicht automatisch gelöscht.

## Externe Dateiänderungen

Der Editor erkennt beim Speichern, wenn die betreffende Datei seit dem Indexieren außerhalb der Anwendung verändert wurde. In diesem Fall wird das Überschreiben verweigert.

Der Index wird beim Prozessstart aufgebaut. Im Google-Modus wird er standardmäßig alle 60 Sekunden erneut aus den Dateien aufgebaut. Das Intervall lässt sich mit `AUTO_RELOAD_INTERVAL` ändern (`30s`, `2m` usw.); Werte unter fünf Sekunden werden abgelehnt. Im Editor erfolgt kein automatischer Reload, damit laufende Bearbeitungen nicht überraschend überlagert werden; dort steht **„Neu einlesen“** zur Verfügung.

## API

Lesend in beiden Modi:

- `GET /api/health`
- `GET /api/config`
- `GET /api/items?q=...&page=1&page_size=60`
- `GET /api/search?q=...&page=1&page_size=20`
- `GET /api/facets?limit=10`
- `GET /api/items/{key}`

Optional bei aktiviertem Ollama-Fallback im Google-Modus:

- `POST /api/ai/fallback` mit `{"query":"..."}` – nur zulässig, wenn die normale KB 0 Treffer liefert
- `GET /api/staging/{key}` – den gerade erzeugten Staging-Entwurf im Viewer laden

Staging-Review im Editor-Modus:

- `GET /api/staging?...` – Staging-Dateien suchen und filtern
- `GET /api/staging/{key}` – Staging-Entwurf laden
- `PUT /api/staging/{key}` – Staging-Entwurf bearbeiten
- `DELETE /api/staging/{key}` – sicher nach `staging/.trash` verschieben
- `POST /api/staging/{key}/promote` – Entwurf nach Produktiv freigeben und Original unter `.approved` archivieren
- `POST /api/staging/bulk` – mehrere Entwürfe mit `action=promote|delete` bearbeiten

Weitere Schreibendpunkte nur im Editor-Modus:

- `PUT /api/items/{key}`
- `POST /api/bulk`
- `POST /api/reload`

Im Google-Modus sind Staging-Liste und sämtliche Staging-Schreib-/Freigabeaktionen sowie die produktiven Schreibendpunkte serverseitig gesperrt.

## Sicherheit

Für interne Remote-Nutzung sollte mindestens Basic Auth aktiviert und die Anwendung hinter einem Reverse Proxy mit TLS veröffentlicht werden.

Das Compose-Setup:

- startet das Container-Root-Filesystem read-only,
- entfernt Linux-Capabilities,
- setzt `no-new-privileges`,
- verwendet `/tmp` als kleines tmpfs,
- kann den Knowledge-Mount im Google-Modus zusätzlich read-only einbinden,
- mountet bei aktiviertem KI-Fallback nur das getrennte Staging-Verzeichnis schreibbar,
- verbindet den Browser nicht direkt mit Ollama.

Die Oberfläche hat keine externen CDN-/JavaScript-Abhängigkeiten.

## Ohne Docker

Voraussetzung: Go 1.23 oder neuer.

Editor:

```bash
APP_MODE=editor go run ./cmd/server -data /pfad/zum/knowledge
```

Google-Modus:

```bash
APP_MODE=google \
APP_TITLE="IT Helpdesk Wissen" \
APP_SUBTITLE="Interne Wissenssuche" \
go run ./cmd/server -data /pfad/zum/knowledge
```

Google-Modus mit Ollama-Fallback:

```bash
APP_MODE=google \
AI_FALLBACK_ENABLED=true \
OLLAMA_BASE_URL=http://127.0.0.1:11434 \
OLLAMA_MODEL=dein-modell \
OLLAMA_TIMEOUT=10m \
STAGING_DIR=/pfad/zum/staging \
go run ./cmd/server -data /pfad/zum/knowledge
```

Tests/Build:

```bash
go test ./...
go vet ./...
go build -o kb-helpdesk ./cmd/server
```

## Projektstruktur

```text
.
├── cmd/server/
│   ├── app.go
│   ├── main.go
│   ├── web/              # Editor-Oberfläche
│   │   ├── index.html
│   │   ├── app.js
│   │   └── style.css
│   └── viewer/           # Google-/Helpdesk-Oberfläche
│       ├── index.html
│       ├── app.js
│       └── style.css
├── internal/aifallback/    # Ollama-Client + Structured Output
├── internal/staging/       # Staging-Suche, Bearbeitung, Soft-Delete und AI-Entwürfe
├── internal/store/
│   ├── store.go
│   └── store_test.go
├── knowledge/
├── staging/
├── backups/
├── Dockerfile
├── docker-compose.yml
├── docker-compose.dual.yml
├── .env.example
├── Makefile
└── go.mod
```
