# Kontrollierte Autonomie und Outcome-gated Learning

Stand: v1.3.0

## Ziel

NeuroForge soll recherchieren und lernen können, ohne KI-Ausgaben automatisch mit bestätigtem Betriebswissen gleichzusetzen. Der Release trennt deshalb drei Vertrauensklassen:

| Klasse | Beispiele | Standard-Trust | Freigabe |
|---|---|---:|---|
| Rohes Modell-/Chat-Signal | `chat.input`, `chat.response` | 0.25 / 0.20 im Controlled Mode | kein automatisches Langzeitlernen |
| Quellengebundene Research-Evidence | `web.search`, `web.page` | 0.45 / 0.60 | Provenance + Dedup + unabhängige Corroboration |
| Menschlich validiertes Helpdesk-Outcome | `glpi.outcome.accepted`, `glpi.outcome.corrected` | 1.00 | explizite Technikeraktion |

Die Werte sind eine Ranking-/Learning-Policy, keine Behauptung absoluter Wahrheit. Auch menschlich bestätigtes Wissen bleibt mit Ticket, Run, Actor und Outcome-ID nachvollziehbar.

## Helpdesk-Lernpfad

```text
GLPI Ticket
   |
   v
Agent analysiert + erzeugt Antwortvorschlag
   |
   v
Techniker prüft
   |--------------------|
   v                    v
bestätigt            korrigiert
   |                    |
   +---------+----------+
             v
      lokales Outcome-Audit
             |
             v
 POST /api/v1/integrations/outcomes
             |
             v
 NeuroForge Semantic Memory
```

Vor dem Persistieren liest der Agent bei aktuellen Runs den Ticketzustand erneut aus GLPI und vergleicht ihn mit `SourceVersion`. Hat sich der entscheidungsrelevante Ticketzustand geändert, wird die Validierung blockiert und ein neuer Agent-Run verlangt.

Nur `accepted` und `corrected` sind zulässig. Der Client kann die vertrauenswürdige Source nicht frei setzen; NeuroForge erzeugt serverseitig `glpi.outcome.accepted` bzw. `glpi.outcome.corrected`.

### Audit und Revisionen

`services/agent` speichert Entscheidungen in `DATA_DIR/ticket-outcomes.json`:

- `pending`: lokal erfasst, Sync noch offen
- `learned`: NeuroForge hat eine Memory-ID bestätigt
- `failed`: Entscheidung bleibt erhalten, Remote-Sync ist fehlgeschlagen
- `supersedes_id`: verweist bei einer späteren Korrektur/Neubewertung auf den vorigen Outcome

Eine exakt wiederholte Entscheidung ist idempotent. Bereits erfolgreich gelernte Outcomes werden nicht ein zweites Mal an NeuroForge gesendet. Ein `failed`-Outcome kann dagegen bewusst erneut synchronisiert werden.

Ab v1.3.0 wird eine Revision auch im NeuroForge-Store wirksam: eine neue Korrektur markiert den Vorgänger atomar als `superseded` und trägt die Revisionskante auf der neuen Memory ein. Supersedete Memories bleiben für Audit/History erhalten, werden aber von semantischer Suche ausgeschlossen.

`OUTCOME_LEARNING_FAIL_OPEN=false` ist der kontrollierte Standard: Ein Remote-Fehler wird dem Techniker sichtbar zurückgegeben. `true` ist nur sinnvoll, wenn lokale Audit-Erfassung wichtiger ist als sofortige zentrale Konsistenz.

## Validierte Erfahrung wiederverwenden

Der geschlossene Lernkreis verwendet aktive menschliche Outcomes bei späteren Tickets als sekundäre Evidenz:

```text
neues Ticket
   |
   +--> offizielle Knowledge-Kandidaten -----------+
   |                                               |
   +--> NeuroForge Outcome Retrieval --------------+
                                                   v
                                          Reply-Auswahl
                                                   |
                                    nur Knowledge-ID aus
                                    offizieller Kandidatenliste
```

Konfiguration:

```env
OUTCOME_RETRIEVAL_ENABLED=true
OUTCOME_RETRIEVAL_SEARCH_K=6
OUTCOME_RETRIEVAL_MIN_SIMILARITY=0.58
OUTCOME_RETRIEVAL_FAIL_OPEN=true
```

Die Outcome-Suche greift ausschließlich auf aktive `glpi.outcome.accepted` und `glpi.outcome.corrected` Memories zu. Der LLM-Systemprompt weist zusätzlich explizit an, dass diese Erfahrungen einen Knowledge-Artikel nur stützen oder widerlegen dürfen. Sie dürfen niemals selbst einen Auto-Reply autorisieren oder eine nicht im Artikel belegte Lösung einführen.

Der Agent protokolliert die verwendeten Outcome-Kandidaten, Similarity, Suchdauer und Fehler pro Run. Prometheus enthält Such-, Treffer-, Fehler- und Learning-Zähler.

## Wirkung messen

`POST /api/quality/replay` ist eine read-only Qualitätsprüfung gegen historische Fälle. Sie meldet Knowledge Recall@K/MRR und Outcome Recall@K/MRR. `experience_rescued_cases` zählt konservativ Fälle, in denen die erwartete offizielle KB nicht in Top-K lag, aber eine aktive validierte Erfahrung die erwarteten Lösungsterme enthielt. Das ist ein Learning-Lift-Indikator, keine automatische Produktionsfreigabe.

Siehe `docs/QUALITY-REPLAY.md`.

## Controlled Learning

`NEUROFORGE_CONTROLLED_LEARNING=true` setzt beim Serverstart eine konservative Policy:

- `learn_chat_inputs=false`
- `learn_chat_responses=false`
- `allow_explicit_learn=true`
- `allow_imports=false`
- `learn_goal_cycles=false`
- höhere Mindestanforderungen für semantische Konsolidierung
- niedriger Trust für Web-/Chat-Signale
- maximaler Trust für explizite GLPI-Outcomes

Damit ist „das Modell hat es gesagt“ kein Lernsignal. Lernen braucht entweder einen expliziten, kontrollierten API-Pfad oder quellengebundene Evidence.

## Research und SearXNG

SearXNG ist im Root-Compose als Profil `research` definiert und wird im normalen `docker compose up` nicht gestartet.

### Research manuell freischalten

1. In `.env` einen zufälligen `SEARXNG_SECRET` setzen. Für reproduzierbare Produktion `SEARXNG_IMAGE` auf einen freigegebenen Tag oder Digest pinnen.
2. Research starten:

```bash
./scripts/research-up.sh
```

Das Script aktiviert für diesen Compose-Aufruf:

```text
NEUROFORGE_RESEARCH_ENABLED=true
NEUROFORGE_SEARXNG_ENABLED=true
```

Es aktiviert **nicht** automatisch `NEUROFORGE_AUTONOMY_ENABLED`.

### Autonomie bewusst separat aktivieren

Für zeitgesteuerte, selbstinitiierte Goal-Cycles zusätzlich in `.env`:

```text
NEUROFORGE_AUTONOMY_ENABLED=true
```

Ein Goal muss zusätzlich `auto_run`/Research erlauben. Damit sind die infrastrukturelle Suchfähigkeit, manuelles Research und zyklische Autonomie getrennt kontrollierbar.

## Research-Vertrauen

Research-Inhalte werden als untrusted external data behandelt. NeuroForge hält Source-URI, Source-ID, Hash, Retrieval-Zeitpunkt und Evidence-Quellen fest. Ähnliche Evidence aus einer weiteren unabhängigen Source erhöht `EvidenceCount` und Confidence über die Corroboration-Logik, statt einen einzelnen Treffer sofort auf Trust 1.0 zu setzen.

Research darf außerdem nicht direkt produktive Knowledge-Artikel veröffentlichen. Der vorhandene Maschinenpfad endet beim token-geschützten KB-Staging; `auto_reply=false` wird dort serverseitig erzwungen. Promotion bleibt eine menschliche Entscheidung.

## Empfohlener Produktionsmodus

```text
NEUROFORGE_CONTROLLED_LEARNING=true
OUTCOME_LEARNING_ENABLED=true
OUTCOME_LEARNING_FAIL_OPEN=false
NEUROFORGE_RESEARCH_ENABLED=false
NEUROFORGE_SEARXNG_ENABLED=false
NEUROFORGE_AUTONOMY_ENABLED=false
```

Research anschließend gezielt aktivieren, beobachten und erst danach – falls gewünscht – Autonomy einschalten.
