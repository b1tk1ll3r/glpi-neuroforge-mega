# Hotfix: Prioritätsbelege und Kategorie-Mapping-Diagnose

Dieser Stand behebt eine semantische Schwäche des separaten Prioritätslaufs.

## Prioritätsanalyse `priority-v3`

Vor dem Ollama-Aufruf werden ausschließlich explizite Aussagen aus Betreff und Tickettext als konservative Belege extrahiert. Beispiele:

- `meine Kollegen und ich` -> `multiple_users_affected`
- `die Bürodrucker laufen noch` -> `workaround_available`
- `gesamter Standort` -> `site_affected`
- `kein Workaround` -> `no_workaround`

Diese Belege entscheiden nicht selbst über die Priorität. Sie werden im Input-Snapshot unter `deterministic_evidence` gespeichert und verhindern lediglich widersprüchliche Modellausgaben.

Ein Modellresultat wird erneut angefordert, wenn es beispielsweise trotz eines expliziten Mehrbenutzer-Belegs `insufficient_information` ausgibt oder wenn `affected_scope` und `reason_codes` nicht zusammenpassen. Nach Ausschöpfung von `OLLAMA_JSON_RETRIES` schlägt nur der Prioritätslauf fehl; Kategorie und Antwortpfad bleiben fail-closed funktionsfähig.

Die Diagnose speichert und zeigt nun zusätzlich:

- `ai_recommended_impact`
- `ai_recommended_urgency`
- `priority_affected_scope`
- `priority_time_criticality`

Eine Ausweichmöglichkeit kann trotz mehrerer Betroffener weiterhin zu einer unveränderten Priorität führen. Der Hotfix erzwingt daher keine Erhöhung, sondern nur eine sachlich konsistente Begründung.

## Kategorie-Mapping-Diagnose

Wenn ein Kategorisierungs-Wissenseintrag auf eine GLPI-ID gemappt wird, deren Name deutlich vom externen Auswahlziel abweicht, erscheint ein nicht blockierender Warnhinweis `category_external_mapping_review`.

Beispiel:

```text
Drucken, Scannen und Kopieren > Netzwerkdrucker -> #67 Arbeitsplatzdrucker
```

Solche Mappings können organisatorisch beabsichtigt sein. Sie beeinflussen jedoch Hints, Kandidaten und die KI-Begründung und sollten deshalb bewusst geprüft werden.
