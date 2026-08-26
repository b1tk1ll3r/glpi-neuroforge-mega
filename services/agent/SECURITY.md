# Security model

## Prioritäts- und Eskalationsentscheidungen

Priorität und Eskalation folgen demselben Grundsatz wie Kategorie und Antwort: Das LLM ist ausschließlich beratend und besitzt keinen GLPI-Toolzugriff. Es liefert strukturierte Empfehlungen mit einem kontrollierten Grundcode-Vokabular. Deterministische Go-Policies validieren Confidence, Reason-Code-Allowlist, zulässige Eskalationsstufe und Aktion sowie den aktuellen Ticketzustand.

Zusätzliche Schutzmaßnahmen:

- Prioritäten werden automatisch nur erhöht, niemals herabgesetzt.
- Die Erhöhung pro Ticketlauf ist durch `PRIORITY_MAX_INCREASE` begrenzt.
- Vor jedem Live-Write wird das Ticket erneut geladen; bei verändertem Source-Hash wird der Write verworfen.
- Eskalationsprüfungen laufen zeitgesteuert, die Ausführung identischer Stufen wird jedoch über einen in `DATA_DIR/state-index.json` persistierten Idempotenzschlüssel dedupliziert. Die Datei ist abgeleiteter, aber sicherheitsrelevanter Betriebszustand und muss zusammen mit `runs.jsonl` geschützt und gesichert werden.
- Menschliche Followups werden von Followups des dedizierten Agent-Benutzers unterschieden. Ein Konflikt mit menschlicher Aktivität blockiert insbesondere den Grund `no_human_response`.
- `AUTO_PRIORITY` und `AUTO_ESCALATION` sind standardmäßig deaktiviert; `DRY_RUN=true` blockiert Live-Writes zusätzlich.
- Jede Eskalationsaktion besitzt eine eigene Allowlist-, Ziel-, Grundcode-, Stufen- und Idempotenzprüfung. Zuweisungen ergänzen vorhandene Akteure, Security-Zuweisungen verlangen einen expliziten Sicherheitsgrund, und Major-Incident-Verknüpfungen benötigen einen deterministisch ausgewählten Kandidaten sowie einen konfigurierten API-Adapter.
- Webhook-Token werden ausschließlich im Connector verwendet und weder an das Modell noch an Status- oder Diagnoseendpunkte ausgegeben. Ausgehende Webhooks tragen einen Idempotenzschlüssel.

Jeder Schritt besitzt einen separaten Auditdatensatz mit Prompt-Version, Input-Hash, strukturiertem Ergebnis, Policy-Prüfungen und Action-Audit. Die Input-Snapshots können Ticket- und Kontextinhalte enthalten; `DATA_DIR` ist daher wie Supportdaten mit personenbezogenen oder vertraulichen Informationen zu behandeln. Damit können Entscheidungen geprüft werden, ohne Analysearten miteinander zu vermischen.

## Trust boundaries

1. **Ticket content is untrusted.** It can contain prompt injection, HTML, links and attacker-controlled instructions.
2. **Knowledge files are trusted operator content.** Only reviewed files should be mounted into `knowledge/`.
3. **The LLM is advisory.** It never receives a callable GLPI tool. All writes are performed by deterministic Go code after policy checks.
4. **GLPI is the source of truth.** The ticket and followups are re-read immediately before writes.
5. **Operational context is read-only and treated as data.** Change descriptions, incident text, asset names and monitoring messages may still contain untrusted text and never become executable instructions.
6. **Uptime Kuma credentials stay in the connector.** API keys are used only for the HTTP request and are never included in the LLM prompt or audit payload.

## Auto-reply gates

An automatic response is only possible when all of these are true:

- `DRY_RUN=false`
- `AUTO_REPLY=true`
- no followup existed at the first check
- either the model selects a Knowledge ID from the provided reply candidates **or** the optional status-association model selects one provided Uptime-Kuma candidate
- for normal replies, the Knowledge ID was in the retrieval result and `knowledge.auto_reply=true`
- for normal replies, global/per-document similarity thresholds and configured category restrictions pass
- for status replies, relevance, KI-Confidence and `Relevanz × KI-Confidence` pass independently and the corresponding operator template is configured
- the applicable reply-confidence gates pass
- the ticket has not changed during inference (including requester/item relations relevant to context)
- enabled context sources completed successfully when `CONTEXT_BLOCK_AUTO_REPLY_ON_ERRORS=true`
- no relevant central Major Incident/Uptime outage is present when `CONTEXT_BLOCK_AUTO_REPLY_ON_INCIDENT=true`
- a second followup check immediately before POST is still empty

The actual user-facing answer comes from reviewed operator content, not generated free text. Normal replies use approved Knowledge JSON. Optional Uptime-Kuma status replies use one of two operator-defined environment templates; the model can only select a supplied monitoring candidate and return a confidence.

## Known concurrency boundary

Without a GLPI API primitive that atomically combines "no followup exists" and "create followup", a small race remains between the final GET and POST. The application minimizes this using a per-ticket process lock and a final followup recheck. Run one active application replica unless you replace the local queue/state/lock with distributed coordination.

## Deployment checklist

- Use a dedicated GLPI service account and least privileges.
- Use HTTPS for GLPI; plain HTTP requires an explicit unsafe override.
- Keep the dashboard bound to localhost/private networks behind TLS.
- Use strong Basic Auth credentials or put the dashboard behind your SSO reverse proxy.
- Keep `/metrics` and health endpoints on a trusted network.
- Keep `.env` outside source control and restrict filesystem permissions.
- Start with `DRY_RUN=true`; review priority and escalation in Shadow Mode before enabling either automatic write path.
- Grant the dedicated GLPI account only those write permissions needed by the explicitly enabled actions: priority changes, actor assignment, private followups and/or ITIL links.
- Protect and back up both `data/runs.jsonl` and `data/state-index.json`; both can contain security-relevant audit or operational state.
- Review GLPI audit logs regularly.


## Kommunikations- und Quellenpolicy

- `KNOWLEDGE_ALLOWED_SOURCES` ist eine fail-closed Allowlist. Nur Dokumente mit einem dort genannten `source`-Label werden geladen, indexiert oder an das LLM übergeben.
- `KNOWLEDGE_AUTO_REPLY_SOURCES` ist eine zusätzliche Teilmengen-Allowlist für automatische Antworten. Eine Quelle darf also recherchierbar sein, ohne Schreibrechte auszulösen.
- Knowledge-Dokumente ohne `source` werden beim Start abgelehnt.
- Auto-Replies erfordern passende `language`- und `communication_style`-Metadaten. Die sicheren Defaults sind `de-DE` und `formal`.
- Anrede, Grußformel und Signatur werden außerhalb des LLM in der Go-Policy zusammengesetzt. Das Modell kann diese Werte nicht verändern.
- Die Metadaten sind eine fachliche Freigabeerklärung. Ein falsch als `de-DE/formal` gekennzeichneter Text wird nicht semantisch durch einen zweiten externen Dienst überprüft; deshalb müssen Auto-Reply-Dokumente weiterhin redaktionell geprüft werden.


## Operational context policy

- Change Calendar, Major Incident, Uptime Kuma and user/device integrations are **read-only**. They do not expand GLPI write capabilities.
- A configured context-source failure is fail-closed for automatic replies by default. This prevents the agent from sending an individual troubleshooting answer while central-service context is unavailable.
- Relevant Major Incidents and Uptime Kuma outages suppress normal Auto-Replies by default. They do not automatically close, merge or reassign tickets.
- Optional status replies are deterministic templates. Require independent relevance, AI-confidence and product-score thresholds; never insert model-authored prose into these templates.
- `GLPI_MAJOR_INCIDENT_FILTER` is operator-controlled. Keep `MAJOR_INCIDENTS_ENABLED=false` until the query has been verified against the target GLPI instance.
- Asset lookup paths and filters are operator-controlled and validated where possible against GLPI's generated OpenAPI route list. Field/filter semantics still need Shadow-Mode verification on the real instance.
- Prefer Uptime Kuma `UPTIME_KUMA_MODE=metrics` for private monitoring. Store `UPTIME_KUMA_API_KEY` as a secret and give the key only the access needed for metrics. `status_page` mode should be used only for information safe to publish on that status page.
- Do not place passwords, tokens, personal secrets or raw diagnostic dumps into Change/Incident descriptions merely because the agent can read them; relevant text may be passed to the local Ollama model.

## Dashboard-Schreibfunktionen

`KNOWLEDGE_WEB_EDIT_ENABLED=true` darf nicht zusammen mit `WEB_ALLOW_ANONYMOUS=true` verwendet werden; die Konfiguration wird beim Start abgelehnt. Mutierende Dashboard-Endpunkte verlangen zusätzlich den Same-App-Request-Header `X-Requested-With: GLPI-AI-Agent`. Statische Knowledge-Dateien aus `KNOWLEDGE_DIR` bleiben read-only; Web-Inhalte werden ausschließlich unter `DATA_DIR/knowledge-managed/` persistiert.

Kategorie-Lernen ist Human-in-the-loop: Nur eine ausdrückliche Bestätigung/Korrektur im Dashboard wird als Lernbeispiel gespeichert. Der Agent übernimmt seine eigenen KI-Empfehlungen oder automatisch geschriebenen Kategorien niemals selbständig in den Lernbestand.

## GLPI Knowledge Base Connector

`GLPI_KB_ENABLED=true` creates a read-only synchronization path from the GLPI knowledge base into the local RAG store. The connector never creates, updates or deletes GLPI knowledge articles. GLPI's own authorization for the OAuth service account is the first visibility boundary; only articles returned to that account can enter the local cache/index.

Synchronized GLPI articles are read-only in the agent dashboard. Automatic replies from this source remain disabled unless all of the following are explicitly configured: the source is in `KNOWLEDGE_AUTO_REPLY_SOURCES`, `GLPI_KB_AUTO_REPLY=true`, and the article belongs to a GLPI Knowledge Base category listed in `GLPI_KB_AUTO_REPLY_CATEGORY_IDS`. In addition, the connector requires a GLPI KB-category -> ITIL-category mapping before marking an imported article as auto-reply eligible.

The normalized cache is stored in `DATA_DIR/glpi-kb-cache.json`; embeddings remain in `DATA_DIR/embeddings.json`. Treat both as potentially sensitive support data and protect/backup `DATA_DIR` accordingly.


## Rich Text aus GLPI KB

Das Feld `answer_html` wird ausschließlich vom read-only GLPI-KB-Synchronisierer befüllt. Web-verwaltete Knowledge-Einträge können dieses Feld nicht setzen. Rich HTML wird weder an Ollama übertragen noch für Embeddings verwendet. Beim Schreiben eines Followups wird das von derselben GLPI-Instanz gelieferte Rich-Text-Markup an GLPI zurückgegeben; GLPI behält seine eigene serverseitige Rich-Text-/HTML-Validierung bei.


## Dynamische Begrenzung des LLM-Kontexts

Knowledge-Kandidaten werden nicht allein anhand einer festen Anzahl in den Modellkontext übernommen. Der Agent kombiniert einen absoluten Retrieval-Floor, einen maximalen Abstand zum besten Treffer und eine harte Obergrenze. Dadurch werden bei großen Wissensbeständen schwache, themenfremde Artikel aus dem Ollama-Prompt herausgehalten, bleiben aber optional im Audit sichtbar.

## Ollama-Pool und Netzwerkgrenze

Mehrere Ollama-Instanzen bilden eine zusätzliche interne Trust Boundary. Der Agent sendet Ticket-, Knowledge- und Kontextauszüge an jeden Node, der einen Request übernehmen kann. Deshalb dürfen ausschließlich administrierte Systeme in `OLLAMA_URLS` aufgenommen werden.

- Ollama-Port 11434 nur von der Agent-IP beziehungsweise dem Agent-Subnetz zulassen.
- Nodes nicht aus Benutzer-VLANs und niemals direkt aus dem Internet erreichbar machen.
- Bei standortübergreifender Verbindung VPN oder einen TLS-Reverse-Proxy mit Netzwerk-/IP-Allowlist verwenden.
- Auf allen Nodes dieselben Chat- und Embedding-Modelle installieren. `OLLAMA_REQUIRE_SAME_MODEL_DIGEST=true` lässt den Pool bei divergierenden Digests fail-closed.
- Node-URLs, Namen und Modelldigests erscheinen in der Betriebsdiagnose. Keine Zugangsdaten in URLs einbetten.
- Failover wiederholt ausschließlich den noch nicht akzeptierten Inferenzrequest. GLPI-Schreiboperationen erfolgen erst nach dem vollständigen KI-Lauf und den deterministischen Policies.
- `OLLAMA_NODE_MAX_INFLIGHT=1` ist für integrierte GPUs und gemeinsam genutzten RAM der sichere Ausgangswert.

## GLPI-KB-Auto-Reply-Freigabe

Die Grundfreigabe synchronisierter GLPI-Wissensartikel ist fail-closed und von der fachlichen Ticketpassung getrennt:

- Kategorisierte Artikel benötigen eine GLPI-Knowledge-Base-Kategorie aus `GLPI_KB_AUTO_REPLY_CATEGORY_IDS`.
- Unkategorisierte Artikel benötigen `GLPI_KB_AUTO_REPLY_ALLOW_UNCATEGORIZED=true` und ihre konkrete `KnowbaseItem`-ID in `GLPI_KB_AUTO_REPLY_UNCATEGORIZED_ARTICLE_IDS`.
- ITIL-/Ticketkategorien sind keine Freigabeschranke und `GLPI_KB_AUTO_REPLY_ITIL_CATEGORY_IDS` wird ignoriert.
- Ein vorhandenes ITIL-Mapping darf nur Retrieval, Evidenz und die separate fachliche Kategoriepassung beeinflussen.
- Der GLPI-KB-Cache enthält eine Policy-Version und einen Hash der Freigabekonfiguration. Veraltete oder mit einer anderen Allowlist erzeugte Caches werden nicht geladen.
