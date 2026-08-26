# NeuroForge v0.5.0 Benchmark

Dieses Dokument enthält die während des v0.5-Release-Builds gemessenen synthetischen Benchmarks. Die Werte sind Maschinen-/Container-spezifisch und keine allgemeine Leistungszusage.

## Ziel

v0.5 trennt zwei Skalierungsfragen bewusst:

1. **Storage/Tiering:** Können Segment-Store, WAL-Batching, katalogfreie Checkpoints und Hot/Cold-Tiering sehr große Memory-Mengen verwalten?
2. **Full ANN:** Wie schnell sind Ingest und Recall, wenn jeder Vektor zusätzlich in den aktuellen reinen Go-HNSW-Graphen aufgenommen wird?

Der Storage-Modus deaktiviert HNSW. Ein 1-Mio-Storage-Lauf ist deshalb **kein** 1-Mio-HNSW-Benchmark.

## Benchmark-Befehl

```bash
go run ./cmd/bench [flags]
```

Wichtige Flags:

```text
-mode full|storage
-memories N
-dim D
-batch N          # max. 4096
-queries N
-k N
-durable=true|false
-tier-every N     # storage mode
-data PATH
-keep
```

Der Benchmark verwendet deterministisch erzeugte, normalisierte float32-Vektoren. Im Benchmark ist `-durable=false` der Default: WAL-fsync pro Batch wird ausgeschaltet, während der Memory-Segment-Batch weiterhin synchronisiert wird. Der normale Serverbetrieb nutzt standardmäßig `storage.wal_sync=true`.

## Gemessene Ergebnisse

### 1.000.000 Memories – Storage/Tiering, 8D, kein HNSW

Aufruf sinngemäß:

```bash
go run ./cmd/bench \
  -mode storage \
  -memories 1000000 \
  -dim 8 \
  -batch 2048 \
  -tier-every 100000
```

Ergebnis:

```text
Memories:              1,000,000
Dimensionen:           8
Ingest:                31.673 s
Ingest-Durchsatz:      31,572.48 Memories/s
Checkpoint:            0.000199 s
Disk:                  976,208,146 Bytes
HeapAlloc:             742,607,464 Bytes
Go Sys:                1,542,834,544 Bytes
Max RSS:               ~1,486,884 KB
Cold Memories:         804,348
Hot Memories:          195,652
Hot Bytes:             67,108,636
Page-Cache Limit:      67,108,864 Bytes
HNSW Nodes:            0
Wall time:             ~32.97 s
```

Der nahezu konstante Checkpoint ist die zentrale v0.5-Verbesserung: `state.json` serialisiert nicht mehr eine Million Memory-Metadatensätze; der Katalog wird aus Segmenten rekonstruiert.

### 250.000 Memories – Storage/Tiering, 8D

```text
Memories:              250,000
Ingest:                7.414 s
Ingest-Durchsatz:      33,720 Memories/s
Checkpoint:            0.000438 s
Disk:                  243,729,783 Bytes
HeapAlloc:             195,022,968 Bytes
Go Sys:                470,811,760 Bytes
Cold Memories:         54,214
Hot Limit:             64 MiB
Max RSS:               ~438,616 KB
Wall time:             ~8.12 s
```

### 50.000 Memories – Full HNSW, 32D

Aufruf sinngemäß:

```bash
go run ./cmd/bench \
  -mode full \
  -memories 50000 \
  -dim 32 \
  -batch 512 \
  -queries 100
```

Ergebnis:

```text
Memories/HNSW nodes:   50,000
Dimensionen:           32
Ingest:                29.278 s
Ingest-Durchsatz:      1,707.78 Memories/s
Checkpoint:            2.111 s
Queries:               100
Recall p50:             0.451 ms
Recall p95:             0.710 ms
Recall p99:             0.747 ms
HeapAlloc:             171,254,392 Bytes
Go Sys:                576,202,912 Bytes
Disk:                  160,123,205 Bytes
Max RSS:               ~553,132 KB
Wall time:             ~31.72 s
```

### 100.000 Memories – Full HNSW, 32D

Der entsprechende Full-ANN-Lauf überschritt in der verwendeten Release-Umgebung das **120-s-Ausführungslimit** des Werkzeugs und wurde deshalb nicht als abgeschlossener Messpunkt gewertet.

Das ist ein wichtiges Ergebnis: Der aktuelle Engpass liegt bei großen Mengen nicht mehr primär im Segment-/Checkpoint-Pfad, sondern im seriellen reinen Go-HNSW-Aufbau. v0.5 behauptet daher bewusst kein „1 Mio live HNSW“-SLA.

## Interpretation

- Der Storage-Pfad skaliert deutlich besser als v0.4, weil Checkpoints O(N)-Memory-Metadaten nicht mehr serialisieren.
- Hot/Cold reduziert residente Body-Duplikate und begrenzt Cold-Read-Caching.
- HNSW-Vektoren und Graph bleiben im RAM.
- Der ANN-Build muss für deutlich größere Live-Indizes weiter optimiert oder partitioniert werden.
- Kurze synthetische Texte und 8D/32D sind nicht repräsentativ für reale 768D/1536D-Embeddings; reale Disk-/RAM-Kosten steigen entsprechend.

## Reproduzierbarkeit

Für vergleichbare Messungen:

- gleicher Go-Compiler
- gleiches Dateisystem/Storage-Medium
- gleiche Dimensionen, HNSW-Parameter und Batch-Größe
- gleiche `-durable`-Einstellung
- ausreichend freier RAM ohne Swap-Thrashing
- CPU-Power-/Container-Limits dokumentieren

Für Produktionsplanung immer mit der tatsächlich verwendeten Embedding-Dimension und realistischen Textgrößen benchmarken.
