# Betrieb mit mehreren Ollama-Instanzen

Der Agent kann bis zu 64 voneinander unabhängige Ollama-Server als gemeinsamen Inferenz-Pool verwenden. Jeder Node lädt das vollständige Chat- und – sofern für RAG erforderlich – Embedding-Modell lokal. Der Pool erhöht damit den **Gesamtdurchsatz und die Ausfallsicherheit**; er teilt ein einzelnes Modell nicht über mehrere Rechner auf.

## Architektur

```text
GLPI AI Agent
  Queue / Worker / Policies
          |
          v
  Ollama Pool Router
   |       |       |
 Node 1  Node 2  Node 3
```

Jeder logische KI-Lauf – Kategorie, Priorität, Status, Antwort oder Eskalation – wird einem verfügbaren Node zugewiesen. Bei retryfähigen Netzwerk- oder Serverfehlern kann derselbe Request auf einem anderen kompatiblen Node wiederholt werden.

## Voraussetzungen je Node

Auf allen Nodes sollten installiert sein:

```text
Chat-Modell:      OLLAMA_MODEL
Embedding-Modell: OLLAMA_EMBEDDING_MODEL
```

Bei `OLLAMA_REQUIRE_SAME_MODEL_DIGEST=true` prüft der Agent über `/api/tags`, dass alle erreichbaren Nodes exakt dieselben Modelldigests melden. Schon ein abweichender Digest macht den gesamten divergierenden Pool fail-closed, damit identische Tickets nicht aufgrund verschiedener Modellstände unterschiedlich bewertet werden.

Für Lenovo-Systeme mit integrierter Radeon-Grafik und gemeinsamem RAM ist als Ausgangspunkt sinnvoll:

```env
OLLAMA_NODE_MAX_INFLIGHT=1
OLLAMA_ROUTING_MODE=least_inflight
OLLAMA_KEEP_ALIVE=10m
OLLAMA_THINK=false
```

Der Gesamtdurchsatz wird zusätzlich durch `WORKERS` begrenzt. Mit drei Nodes und `WORKERS=2` können höchstens zwei Ticketpipelines gleichzeitig Inferenz anfordern. Für einen Lasttest mit drei gleichartigen Nodes ist daher beispielsweise sinnvoll:

```env
WORKERS=3
OLLAMA_NODE_MAX_INFLIGHT=1
```

Die Analysestufen eines einzelnen Tickets bleiben aus fachlichen Gründen weitgehend geordnet. Der größte Poolnutzen entsteht deshalb bei mehreren gleichzeitig wartenden Tickets oder Eskalationsläufen.

## Minimale Pool-Konfiguration

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

`OLLAMA_FAILOVER_ATTEMPTS=0` bedeutet: maximal alle konfigurierten Nodes versuchen.

## Routing-Modi

### `least_inflight`

Empfohlener Standard. Der Node mit den wenigsten laufenden Requests wird bevorzugt. Bei gleicher Auslastung wird zunächst der bislang seltener verwendete Node gewählt; anschließend dienen mittlere Laufzeit und Name als stabile Tie-Breaker. Dadurch verteilt sich auch serieller Verkehr über gleichartige Nodes.

```env
OLLAMA_ROUTING_MODE=least_inflight
```

### `round_robin`

Requests werden zyklisch verteilt. Dieser Modus ist einfach, berücksichtigt aber Leistungsunterschiede nur begrenzt.

```env
OLLAMA_ROUTING_MODE=round_robin
```

### `weighted`

Geeignet für gemischte Hardware. Die Gewichte stehen positionsgleich zu `OLLAMA_URLS`.

```env
OLLAMA_URLS=http://lenovo-1:11434,http://lenovo-2:11434,http://gpu-server:11434
OLLAMA_NODE_NAMES=lenovo-1,lenovo-2,gpu-server
OLLAMA_NODE_WEIGHTS=1,1,6
OLLAMA_ROUTING_MODE=weighted
```

### `fastest_recent`

Bevorzugt Nodes mit der niedrigsten gleitenden mittleren Request-Laufzeit. Neue oder zurückgekehrte Nodes ohne Messwert werden zunächst einmal vermessen, damit sie nicht dauerhaft verhungern.

```env
OLLAMA_ROUTING_MODE=fastest_recent
```

## Startverhalten und Docker Compose

Der Webserver startet unabhängig vom Pool. Vor Knowledge-Initialisierung und Ticketverarbeitung wartet der Agent wiederholt auf mindestens einen gesunden, kompatiblen Ollama-Node. Ein noch bootender Node führt dadurch nicht mehr zu einem einmaligen dauerhaften Knowledge-Fehler; im Dashboard bleibt der Zustand währenddessen sichtbar.

Die Compose-Dateien besitzen keine harte Abhängigkeit des Agenten vom mitgelieferten `ollama`-Service mehr. Für ausschließlich externe Nodes kann gezielt nur der Agent gestartet werden:

```bash
docker compose up -d agent
```

`OLLAMA_URLS` hat Vorrang vor dem weiterhin aus Kompatibilitätsgründen gesetzten `OLLAMA_URL=http://ollama:11434`. Bei `docker compose up -d` ohne Servicenamen wird der gebündelte lokale Ollama-Service weiterhin mitgestartet, aber nur verwendet, wenn seine URL im effektiven Pool steht.

## Healthchecks und Cooldown

```env
OLLAMA_NODE_HEALTH_INTERVAL=15s
OLLAMA_NODE_FAILURE_COOLDOWN=30s
OLLAMA_NODE_REQUEST_TIMEOUT=10m
```

Der Healthcheck ruft `/api/tags` auf und prüft:

- HTTP-Erreichbarkeit,
- Vorhandensein des Chat-Modells,
- Vorhandensein des Embedding-Modells,
- Modelldigests,
- Kompatibilität mit den übrigen Nodes.

Ein retryfähiger Fehler setzt den betroffenen Node in einen Cooldown. Währenddessen erhält er keine neuen Requests. Ein späterer erfolgreicher Healthcheck macht ihn wieder sichtbar; der Cooldown läuft dennoch bis zu seinem Ende, um Flapping zu dämpfen.

## Failover

Failover wird ausgelöst bei:

- Verbindungsfehlern,
- Zeitüberschreitungen,
- HTTP 408,
- HTTP 429,
- HTTP 5xx,
- ungültigem äußerem Ollama-Response-JSON.

```env
OLLAMA_FAILOVER_ENABLED=true
OLLAMA_FAILOVER_ATTEMPTS=0
```

Nicht retryfähige 4xx-Fehler werden nicht auf andere Nodes gespiegelt. Die Modellaufrufe sind rein lesende Inferenzaufrufe; GLPI-Schreibaktionen erfolgen erst später durch die deterministische Go-Policy.

## Analyse-Diagnose

Jeder `AnalysisRun` speichert unter `provider`:

```json
{
  "provider": "ollama-pool",
  "routing_mode": "least_inflight",
  "selected_node": "lenovo-02",
  "selected_url": "http://10.20.30.22:11434",
  "failover_used": true,
  "attempt_count": 2,
  "attempts": [
    {
      "attempt": 1,
      "stage": "priority",
      "node_name": "lenovo-01",
      "outcome": "error",
      "retryable": true
    },
    {
      "attempt": 2,
      "stage": "priority",
      "node_name": "lenovo-02",
      "outcome": "success"
    }
  ]
}
```

Zusätzlich werden – sofern Ollama sie liefert – Ladezeit, Prompt-Tokens, Generierungstokens und zugehörige Laufzeiten gespeichert.

## Dashboard und Prometheus

`/api/status` enthält unter anderem:

```text
ollama_nodes
ollama_node_count
ollama_healthy_nodes
ollama_available_nodes
ollama_routing_mode
```

Prometheus exportiert pro Node:

```text
glpi_agent_ollama_node_healthy
glpi_agent_ollama_node_available
glpi_agent_ollama_node_inflight
glpi_agent_ollama_node_requests_total
glpi_agent_ollama_node_failures_total
glpi_agent_ollama_node_average_duration_ms
```

## Netzwerksicherheit

Ollama besitzt an seiner lokalen API üblicherweise keine eigene Mandantenauthentifizierung. Die Nodes sollten daher:

- in einem eigenen Server-/KI-Netz liegen,
- Port 11434 nur vom GLPI-AI-Agent-Host akzeptieren,
- nicht aus Benutzer-VLANs erreichbar sein,
- niemals direkt aus dem Internet erreichbar sein,
- bei standortübergreifender Nutzung über VPN oder einen TLS-Reverse-Proxy mit Netzwerk-/IP-Allowlist angebunden werden.

Beispiel auf jedem Node:

```env
OLLAMA_HOST=0.0.0.0:11434
```

Diese Freigabe allein ist nicht ausreichend; eine Host- oder Netzfirewall muss den Zugriff auf die Agent-IP begrenzen.

## Rollout-Empfehlung

1. Auf allen Nodes identische Ollama- und Modellstände installieren.
2. Chat- und Embedding-Modell einmal lokal laden.
3. Jeden Node einzeln mit `/api/tags` prüfen.
4. Pool zunächst mit `OLLAMA_REQUIRE_SAME_MODEL_DIGEST=true` starten.
5. Im Dashboard kontrollieren, dass alle Nodes `healthy=true` und `compatible=true` melden.
6. `OLLAMA_NODE_MAX_INFLIGHT=1` beibehalten und mehrere Testtickets parallel analysieren.
7. Erst nach Messung von RAM, Temperatur und Laufzeiten höhere Parallelität testen.

## Modellupdates bei strikter Digest-Prüfung

Bei `OLLAMA_REQUIRE_SAME_MODEL_DIGEST=true` ist ein gemischter Modellstand absichtlich nicht verfügbar. Ein Pull oder Austausch nur auf einem einzelnen aktiven Node kann den Pool deshalb beim nächsten Healthcheck fail-closed setzen. Sichere Varianten sind:

1. Agent in ein Wartungsfenster nehmen und das Modell auf allen Nodes aktualisieren.
2. Einen neuen, eindeutig versionierten Modelltag zunächst auf allen Nodes bereitstellen und erst danach `OLLAMA_MODEL` zentral umstellen.
3. Für Hardwarewartung einen Node aus `OLLAMA_URLS` entfernen, Agent neu starten und ihn erst mit passendem Digest wieder aufnehmen.

`OLLAMA_REQUIRE_SAME_MODEL_DIGEST=false` sollte nicht als normale Rolling-Update-Strategie verwendet werden, weil dann identische Tickets während der Übergangszeit von unterschiedlichen Modellständen bewertet werden können.

## Rückfall auf einen Einzelnode

Die bisherige Konfiguration bleibt kompatibel:

```env
OLLAMA_URL=http://localhost:11434
OLLAMA_URLS=
```

Ist `OLLAMA_URLS` leer, wird automatisch `OLLAMA_URL` als einzelner Pool-Node verwendet.
