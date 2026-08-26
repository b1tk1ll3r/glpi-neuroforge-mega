# Hotfix: Prioritätsanalyse in Diagnose und Quellpaket

## Fehlerbild

Ein Ticketlauf enthielt weder `priority_analysis_executed` noch `priority_decision`, `priority_checks` oder einen Eintrag mit `analysis_type: "priority"` in `analyses`. In der Diagnose waren deshalb nur Kategorie und Status sichtbar.

## Ursache

Das vorherige Quellarchiv wurde mit einem zu breiten Ausschlussmuster `agent` erstellt. Dadurch fehlten ausgerechnet `cmd/agent` und `internal/agent` im Quellpaket. Wer die übrigen Änderungen über einen bestehenden Projektstand kopierte oder den alten Agent-Einstieg weiterverwendete, erhielt zwar Konfiguration, Modelltypen und UI-Teile, aber nicht die zentrale Ausführung der Prioritäts- und Eskalationsstufen.

## Korrektur

Dieses Paket enthält wieder den vollständigen Quellstand einschließlich `cmd/agent` und `internal/agent`. Neue normale Ticketläufe speichern die Prioritätsentscheidung zusätzlich in zwei Formen:

- gut sichtbare Top-Level-Felder wie `priority_before`, `ai_recommended_priority`, `priority_proposed`, `priority_would_change`, `priority_decision`, `priority_reason_codes` und `priority_checks`,
- einen eigenständigen Eintrag in `analyses` mit `analysis_type: "priority"`, Input-Snapshot, Prompt-Version, Reason Codes, Policy-Prüfungen und Action-Audit.

Die Diagnoseoberfläche zeigt eine eigene Karte **Prioritätsentscheidung** mit dem Ablauf **Aktuell → KI-Empfehlung → Policy-Ziel**. Damit ist auch im Shadow Mode eindeutig sichtbar, ob und auf welchen Wert die Priorität geändert worden wäre und welche Regel eine Änderung gegebenenfalls blockiert hat.

## Wichtig nach dem Austausch

Historische Zeilen in `data/runs.jsonl` werden nicht nachträglich um eine Prioritätsanalyse ergänzt. Nach Neustart muss ein neuer Ticketlauf entstehen. Dafür kann das Ticket geändert, ein Webhook ausgelöst oder eine noch nicht verarbeitete Ticketversion verwendet werden. Bei einem bereits verarbeiteten unveränderten Ticket greift weiterhin die Versions-Deduplizierung.

Empfohlener Shadow Mode:

```env
PRIORITY_ENABLED=true
AUTO_PRIORITY=false
DRY_RUN=true
```

`AUTO_PRIORITY=false` bedeutet nur, dass nicht geschrieben wird. Die KI-Analyse und sämtliche Policy-Prüfungen werden trotzdem ausgeführt und angezeigt.

## Ergänzung: `insufficient_information` ist kein Policy-Fehler

Ein unverändertes Ergebnis wie `#3 → #3` mit dem Reason Code
`insufficient_information` ist eine bewusste Enthaltung der Prioritäts-KI. Dieser
Grund soll keine Höherstufung auslösen, ist aber auch kein verbotener Aktionsgrund.

Der Hotfix unterscheidet deshalb jetzt zwischen:

- **Aktionsgründen**, die eine Erhöhung begründen dürfen und über
  `PRIORITY_ALLOWED_REASON_CODES` freigegeben werden,
- **neutralen Kontext-/Enthaltungsgründen** wie `single_user_affected`,
  `workaround_available` und `insufficient_information`.

Bei unveränderter Empfehlung wird `insufficient_information` als
`priority_no_change_insufficient_information` protokolliert. Confidence und
Reason-Allowlist sind dabei nicht anwendbare Schreib-Gates (`status: "na"`),
weil keine Änderung vorgeschlagen wird. Empfiehlt das Modell trotz
`insufficient_information` eine Erhöhung, blockiert die Policy diese weiterhin
mit `priority_insufficient_information`.

Reason Codes werden vor Policy und Audit getrimmt, kleingeschrieben und
dedupliziert. Damit wird eine Ausgabe wie sechs identische
`insufficient_information`-Einträge als genau ein Grund gespeichert.
