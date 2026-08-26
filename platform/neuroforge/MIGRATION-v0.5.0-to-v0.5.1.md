# Migration NeuroForge v0.5.0 → v0.5.1

v0.5.1 ist ein kompatibler Performance-Patch. REST-API, Memory-Segmentformat und die v0.5-Konfiguration bleiben kompatibel.

## Upgrade

1. NeuroForge v0.5.0 sauber stoppen.
2. Das komplette Datenverzeichnis sichern, insbesondere `memory-segments/`, `wal/`, `cluster-log/` und `hnsw-index/`.
3. Server/Worker durch die v0.5.1-Binaries ersetzen oder den neuen Quellcode bauen.
4. Mit demselben `-data`-Verzeichnis starten.

```bash
go run ./cmd/server -data /pfad/zum/v0.5/data
```

v0.5.1 kann bestehende v0.5.0-JSON-HNSW-Bases lesen. Ein neuer vollständiger HNSW-Base-Checkpoint wird im kompakten binären v0.5.1-Format geschrieben. Memory-Segmente werden nicht neu codiert.

## Rollback

Vor einem Rollback das vor dem Upgrade angelegte Backup wiederherstellen. Insbesondere sollte ein v0.5.0-Prozess nicht auf ein nach v0.5.1 neu geschriebenes `hnsw-index/` angewiesen sein.

## Verhalten, das sich ändert

- HNSW-Level werden deterministisch aus der Memory-ID abgeleitet.
- Der Index normalisiert Vektoren beim Insert einmalig und verwendet intern Dot-Products.
- Construction-Suchen besitzen Worst-Case-Budgets für besuchte Knoten und Greedy-Hops.
- Neue HNSW-Base-Snapshots sind binär; JSON-Deltas bleiben kompatibel/inspectierbar.

An der öffentlichen Memory-/Chat-/Goal-/Cluster-API ändert dieser Patch nichts.
