# Eskalationsfunktionen

Die Eskalation ist ein eigenständiger, zeitgesteuerter KI-Lauf. Sie ist von der normalen Ticketversion-Deduplizierung unabhängig und kann deshalb unveränderte Tickets erneut bewerten. Das Modell darf ausschließlich eine strukturierte Empfehlung aus kontrollierten Aktionen und Grundcodes abgeben. Jede Aktion wird anschließend separat durch Go-Regeln geprüft und erhält einen eigenen Schritt im `ActionAudit`.

## Sicherer Betriebsmodus

Empfohlener Einstieg:

```env
DRY_RUN=true
ESCALATION_ENABLED=true
AUTO_ESCALATION=false
ESCALATION_SCAN_INTERVAL=30m
ESCALATION_MIN_AGE=4h
ESCALATION_MIN_INACTIVITY=2h
ESCALATION_ANALYSIS_TIMEOUT=45s
GLPI_ESCALATION_FILTER=status.id==1
```

`ESCALATION_ENABLED=true` startet Scheduler und KI-Analyse. `AUTO_ESCALATION=false` hält alle Aktionen im Shadow Mode. Ein Live-Write ist nur möglich, wenn zusätzlich `AUTO_ESCALATION=true` und `DRY_RUN=false` gelten.

## Deterministische Belege

Vor dem Modellaufruf berechnet der Agent selbst:

- Ticketalter seit `date_creation`
- Inaktivitätsdauer seit dem letzten nicht-agentischen Followup; sie ist nur für den Grund `no_human_response` ein blockierendes Gate
- fehlende Zuweisung
- SLA-Frist aus `time_to_resolve`
- SLA-Verletzung oder Risiko innerhalb `ESCALATION_SLA_RISK_WINDOW`
- relevantesten Major-Incident-Kandidaten oberhalb `ESCALATION_MAJOR_INCIDENT_MIN_RELEVANCE`

Diese Werte werden im Input-Snapshot unter `evidence` gespeichert. Das Modell darf sie nicht erfinden oder überschreiben.

## Verfügbare Aktionen

### `raise_priority`

Erhöht die aktuelle GLPI-Priorität deterministisch um genau eine Stufe. Herabstufungen und Werte oberhalb 6 sind ausgeschlossen.

Erforderlich:

```env
ESCALATION_ALLOWED_ACTIONS=none,raise_priority
```

### `assign_second_level`

Fügt die konfigurierte Second-Level-Gruppe zu den vorhandenen Ticketzuweisungen hinzu. Vorhandene Gruppen bleiben erhalten. Zulässig ist die Aktion nur bei einem operativen Eskalationsgrund wie fehlender Reaktion, fehlender Zuweisung, SLA-Risiko, fachlicher Frist oder fehlender Ausweichmöglichkeit.

```env
ESCALATION_SECOND_LEVEL_GROUP_ID=42
ESCALATION_ALLOWED_ACTIONS=none,assign_second_level
```

### `assign_security_team`

Fügt die konfigurierte Security-Gruppe hinzu. Die Policy akzeptiert die Aktion ausschließlich zusammen mit `security_incident_suspected`.

```env
ESCALATION_SECURITY_GROUP_ID=51
ESCALATION_ALLOWED_ACTIONS=none,assign_security_team
```

### `notify_service_owner`

Bindet einen Service Owner über eine GLPI-Gruppe, einen GLPI-Benutzer, einen Webhook oder eine Kombination daraus ein. Die Aktion ist erst ab `ESCALATION_SERVICE_OWNER_MIN_LEVEL` zulässig.

```env
ESCALATION_SERVICE_OWNER_MIN_LEVEL=2
ESCALATION_SERVICE_OWNER_GROUP_ID=61
ESCALATION_SERVICE_OWNER_USER_ID=62
ESCALATION_WEBHOOK_URL=https://internal.example/escalations
ESCALATION_WEBHOOK_BEARER_TOKEN=...
ESCALATION_WEBHOOK_ALLOW_INSECURE_HTTP=false
ESCALATION_ALLOWED_ACTIONS=none,notify_service_owner
```

Das Bearer-Token wird nicht über Status- oder Diagnose-API ausgegeben. Der Webhook erhält einen `Idempotency-Key`-Header und ein JSON-Objekt mit Ticket-ID, Stufe, Aktion, Ziel, Confidence, Grundcodes und Begründung.

### `link_major_incident`

Verknüpft das Ticket mit dem deterministisch relevantesten Major-Incident-Kandidaten. Die Policy verlangt:

- `major_incident_candidate`
- aktivierten Major-Incident-Kontext
- einen Kandidaten oberhalb des Relevanzschwellwerts
- einen ausdrücklich konfigurierten GLPI-Linkadapter

```env
CONTEXT_ENABLED=true
MAJOR_INCIDENTS_ENABLED=true
ESCALATION_MAJOR_INCIDENT_MIN_RELEVANCE=0.50
ESCALATION_ALLOWED_ACTIONS=none,link_major_incident
GLPI_ESCALATION_ITIL_LINK_PATH=/INSTALLATIONSSPEZIFISCHER/PFAD/{{ticket_id}}
GLPI_ESCALATION_ITIL_LINK_BODY={"source":{"id":{{ticket_id}}},"target":{"id":{{major_incident_id}}}}
```

Pfad und JSON-Body müssen anhand des API-Vertrags der konkreten Installation gesetzt werden. Ohne beide Werte wird die Aktion nicht an das Modell angeboten und im Live-Modus verweigert die Konfigurationsprüfung den Start.

### `request_manager_review`

Fordert ab `ESCALATION_MANAGER_REVIEW_MIN_LEVEL` eine Management-Prüfung an. Als Ziel können Gruppe, Benutzer und/oder Webhook konfiguriert werden.

```env
ESCALATION_MANAGER_REVIEW_MIN_LEVEL=3
ESCALATION_MANAGER_REVIEW_GROUP_ID=71
ESCALATION_MANAGER_REVIEW_USER_ID=72
ESCALATION_ALLOWED_ACTIONS=none,request_manager_review
```

## Private Eskalationsnotizen

Mit `ESCALATION_ADD_PRIVATE_FOLLOWUP=true` schreibt jede ausgeführte Aktion einen privaten GLPI-Followup. Die Texte sind operatorseitige Templates, nicht frei vom Modell erzeugte Antworten.

Verfügbare Variablen:

- `{{ticket_id}}`
- `{{ticket_name}}`
- `{{level}}`
- `{{action}}`
- `{{reason}}`
- `{{reason_codes}}`
- `{{major_incident_id}}`
- `{{major_incident_name}}`
- `{{major_incident_score}}`

Konfigurierbare Templates:

```env
ESCALATION_SECOND_LEVEL_NOTE=...
ESCALATION_SECURITY_NOTE=...
ESCALATION_SERVICE_OWNER_NOTE=...
ESCALATION_MAJOR_INCIDENT_NOTE=...
ESCALATION_MANAGER_REVIEW_NOTE=...
```

## GLPI-Zuweisungsadapter

Die Namen der Ticketfelder können installationsabhängig sein. Der Agent unterstützt zwei Payload-Formen:

```env
GLPI_ESCALATION_GROUP_PATCH_FIELD=assigned_groups
GLPI_ESCALATION_USER_PATCH_FIELD=assigned_users
```

Pluralfelder erhalten eine Liste von `{ "id": ... }` und sind der empfohlene Adapter, wenn vorhandene Zuweisungen erhalten bleiben sollen. Für die installationsabhängigen Singularfelder `group`, `group_tech`, `user` und `user_tech` wird nur ein einzelnes `{ "id": ... }` gesendet; deren Ergänzungs- oder Ersetzungsverhalten muss deshalb besonders sorgfältig gegen die konkrete GLPI-API geprüft werden. Vor Live-Aktivierung ist ein Shadow- und Testticket-Lauf zwingend.

## Mehrere Aktionen pro Lauf

Das Modell kann höchstens drei Aktionen empfehlen. Jede Aktion besitzt:

- eigenes Ziel
- eigene Policy-Checks
- eigenen Entscheidungscode
- eigenen Idempotenzschlüssel
- eigenen Audit-Schritt mit `proposed`, `executed`, `dry_run`, `before`, `after`, `result` und `error`

Eine Aktion wird nicht freigegeben, nur weil eine andere Aktion im selben Lauf zulässig ist. Beispielsweise kann `assign_second_level` akzeptiert und `assign_security_team` wegen fehlendem Sicherheitsgrund blockiert werden.

## Idempotenz

Erfolgreiche Live-Schritte werden separat in `DATA_DIR/state-index.json` gespeichert. Ein Schlüssel enthält Ticket, Stufe, Aktion und Ziel, zum Beispiel:

```text
ticket=20;level=2;action=assign_second_level;target=group:42
```

Damit kann eine andere Aktion derselben Stufe noch ausgeführt werden, während eine bereits erfolgreiche identische Aktion nicht erneut geschrieben wird. Historische alte Schlüssel im Format `ticket=20;level=2` bleiben für `raise_priority` kompatibel.

## Empfohlene stufenweise Freigabe

1. Nur `raise_priority` im Shadow Mode beobachten.
2. `assign_second_level` mit einer Testgruppe ergänzen.
3. Security-Zuweisung anhand gezielter Testtickets prüfen.
4. Service-Owner und Management zunächst nur per Webhook oder Testziel validieren.
5. Major-Incident-Link erst nach erfolgreichem Test des installationsspezifischen Linkadapters aktivieren.
6. Erst danach `AUTO_ESCALATION=true` und schließlich `DRY_RUN=false` setzen.
