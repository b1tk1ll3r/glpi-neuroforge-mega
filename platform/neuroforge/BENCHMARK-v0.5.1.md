# NeuroForge v0.5.1 HNSW Performance Benchmark

Dieses Dokument misst gezielt den in v0.5.0 identifizierten Full-HNSW-Flaschenhals. Die Werte stammen aus derselben ChatGPT-Containerklasse und sind keine allgemeine Leistungszusage. Dateisystem-Cache, CPU-Sharing und Container-Last erzeugen messbare Laufzeitstreuung; deshalb sind die Zahlen als reproduzierbare Größenordnung und nicht als SLA zu lesen.

## Was geändert wurde

Der v0.5.0-Profiler zeigte die meiste CPU-Zeit in `searchLayerLocked`, `Cosine`, String-Maps und GC. v0.5.1 ändert den Hot Path daher strukturell:

- Integer-Slots statt String-IDs während Graph-Traversal
- einmalige L2-Normalisierung beim Insert, danach Dot-Product im ANN-Graph
- wiederverwendbare Generation-Visit-Arrays statt `map[string]bool`
- typisierte Max-/Min-Heaps statt `container/heap`
- Edge-Similarity wird gespeichert; Pruning berechnet sie nicht erneut
- Neighbor-Slots `uint32 + float32`
- Batch-Insert ohne O(N²)-Capacity-Copy
- begrenzte Construction-Visits und Greedy-Hops
- ID-deterministische Level-Zuweisung
- binäre Base-Snapshots mit numerischen Neighbor-Indizes statt JSON-Neighbor-Strings

Zusätzlich gibt es einen Brute-Force-Qualitätstest; auf dem eingebauten 6k/32D-Test liegt Recall@10 bei etwa `0.98`.

## Benchmark-Befehl

```bash
go run ./cmd/bench \
  -mode full \
  -memories N \
  -dim 32 \
  -batch 256 \
  -queries 100 \
  -tier-every 25000
```

Optional zeigt `-progress-every 25000` Ingest-/Checkpoint-Phasen auf stderr. Wie in v0.5.0 bleibt `-durable=false` im Benchmark der Default; der normale Serverbetrieb verwendet weiterhin standardmäßig `storage.wal_sync=true`.

## Ergebnisübersicht

| Version / Lauf | Memories | Ingest | Memories/s | Checkpoint | Query p95 | HeapAlloc | Go Sys |
|---|---:|---:|---:|---:|---:|---:|---:|
| v0.5.0 Full | 50,000 | 29.278 s | 1,707.78 | 2.111 s | 0.710 ms | 171,254,392 B | 576,202,912 B |
| v0.5.1 Full | 50,000 | 6.569 s | 7,611.83 | 0.271 s | 0.366 ms | 68,944,200 B | 141,469,728 B |
| v0.5.0 Full | 100,000 | >120 s / Timeout | — | — | — | — | — |
| v0.5.1 Full | 100,000 | 16.395 s | 6,099.37 | 0.594 s | 0.801 ms | 137,704,656 B | 279,240,768 B |
| v0.5.1 Full | 200,000 | 71.371 s | 2,802.25 | 1.781 s | 0.928 ms | 269,034,320 B | 521,554,048 B |

Der 50k-Ingest ist damit gegenüber v0.5.0 etwa **4.46x schneller**. Entscheidend ist aber die verschobene Skalierungsgrenze: Der 100k-Lauf, der in v0.5.0 das 120-s-Limit überschritt, ist nun in rund 16.4 s abgeschlossen; 200k bleiben mit rund 71.4 s ebenfalls darunter.

## 200k-Detail

```text
Memories/HNSW nodes:   200,000
Dimensionen:           32
Batch:                  256
Ingest:                 71.371 s
Ingest-Durchsatz:       2,802.25 Memories/s
Checkpoint:             1.781 s
Queries:                100
Query p50:              0.611 ms
Query p95:              0.928 ms
Query p99:              1.589 ms
HeapAlloc:              269,034,320 Bytes
Go Sys:                 521,554,048 Bytes
Disk:                   356,292,106 Bytes
Cold Memories:          47,012
Hot Memories:           152,988
Hot Bytes:              67,108,744
```

## Binär-Snapshot

Der erste große HNSW-Checkpoint war nach der Build-Optimierung der nächste sichtbare Kostenblock. v0.5.1 schreibt die Base daher als binäre per-Dimension-Datei mit numerischen Neighbor-Indizes. Alte v0.5.0-JSON-Bases werden weiterhin gelesen; neue binäre Bases werden beim Restart direkt geladen. Deltas bleiben für Kompatibilität und einfache Inspektion JSON-basiert.

## Was der Benchmark nicht beweist

- 32D ist viel kleiner als typische reale Embeddings mit 768/1024/1536+ Dimensionen.
- HNSW-Vektoren und Graph liegen weiterhin im RAM.
- 200k erfolgreich indexierte synthetische Memories sind kein 1-Mio-SLA.
- Der aktuelle Delta-Snapshot-Vergleich kann bei sehr großen Indizes weiterhin O(N) Arbeit erzeugen; die binäre Base optimiert besonders Initial-/Merge-Snapshots.
- Cluster-Replikation ist in diesen Zahlen nicht enthalten.

Für Produktionsplanung mit den realen Embedding-Dimensionen, Textgrößen, `wal_sync=true`, tatsächlicher Hardware und realistischem Query-Mix messen.
