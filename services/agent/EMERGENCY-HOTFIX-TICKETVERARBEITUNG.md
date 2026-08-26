# Emergency-Hotfix: Ticketverarbeitung wird nicht mehr durch Prioritätsanalyse blockiert

## Symptom

Nach Installation von `priority-v3` kann die Weboberfläche erreichbar sein, während neue Tickets scheinbar nicht mehr verarbeitet werden oder sehr lange in der Queue verbleiben.

## Technische Ursache

Der Prioritätslauf war zwar fachlich optional, verwendete aber den allgemeinen Ollama-Kontext und konnte bei einer semantisch widersprüchlichen Modellantwort einen zweiten Modellaufruf starten. Bei `OLLAMA_MAX_CONCURRENT=1` blockiert ein solcher Aufruf auch die nachfolgenden Kategorie- und Antwortaufrufe anderer Worker. Abhängig von `OLLAMA_TIMEOUT` konnte dies mehrere Minuten dauern.

Der Hotfix macht die Prioritätsanalyse konsequent fail-open:

- eigener Timeout `PRIORITY_ANALYSIS_TIMEOUT`, Standard `45s`,
- kein erneuter Ollama-Aufruf wegen semantischer Inkonsistenzen,
- deterministische Normalisierung von Scope und Reason Codes,
- keine Erhöhung der Modell-Confidence,
- bei Timeout oder Fehler wird nur der Prioritätslauf als `priority_ai_failed` markiert,
- Kategorie-, Status- und Antwortpfad laufen weiter,
- Diagnoseversion `priority-v4`.

## Sofortige Wiederherstellung ohne Update

Bis der Hotfix installiert ist:

```env
PRIORITY_ENABLED=false
```

Danach den Agenten neu starten. Die Kategorisierung und Antwortauswahl funktionieren unabhängig davon weiter.

## Konfiguration nach Installation

```env
PRIORITY_ENABLED=true
AUTO_PRIORITY=false
PRIORITY_ANALYSIS_TIMEOUT=45s
```

Bei langsamer CPU-Inferenz kann der Wert erhöht werden. Er sollte deutlich unter `OLLAMA_TIMEOUT` bleiben.

## Wichtiger Upgrade-Hinweis

Beim nativen Betrieb nur das Programm beziehungsweise die geänderten Quelldateien ersetzen. Nicht löschen oder überschreiben:

- `.env` / `.env_local`
- `data/`
- `knowledge/`

Wenn `data/` entfernt wurde, muss der Knowledge-Index neu aufgebaut werden. Während der Initialisierung bleibt die Ticketverarbeitung absichtlich pausiert. Im Dashboard sind dann `knowledge_ready=false` und der aktuelle Initialisierungsstatus sichtbar.

## Diagnose, falls weiterhin keine Tickets verarbeitet werden

Mit `PRIORITY_ENABLED=false` neu starten. Wenn weiterhin kein neuer Lauf entsteht, liegt die Ursache nicht im Prioritätslauf. Dann sind insbesondere zu prüfen:

- `knowledge_ready`
- `knowledge_init_state`
- `knowledge_init_error`
- `glpi_ok`
- `ollama_ok`
- `queue_depth`
- Startprotokoll ab `knowledge initialization started in background`

