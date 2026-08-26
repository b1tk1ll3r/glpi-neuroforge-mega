# SQAR → NeuroForge: gezielte Vector-Journal-Migration

## Entscheidung

Der SQAR-PoC passt **nicht** sinnvoll als pauschale Kompressionsschicht über den gesamten NeuroForge-Storage.

- `memory-segments/*.nfs` brauchen unabhängige Records, mmap und gezielten Random Access. Eine Archiv-/Chunk-Kompression über ganze Segmente würde diese Eigenschaften verschlechtern.
- `state.json`, WAL und Cluster-Log sind Kontroll-/Durability-Pfade; zusätzliche adaptive Suche erhöht dort Latenz und Fehleroberfläche ohne klaren Nutzen.
- `vector-journal.nfv` ist dagegen ein rebuildbarer, sequenziell gelesener Binär-Cache mit vielen gleichdimensionierten Float32-Vektoren. Genau dort kann die SQAR-Idee (2D-Anordnung, reversible Residuen, alternative Traversierung vor Entropie-Coding) Struktur sichtbar machen.

Daher wurde nur der für diesen Datenpfad sinnvolle Teil migriert.

## Was migriert wurde

Neues Journalformat `NFVJ2`:

1. Vektoren gleicher Dimension werden in Blöcke gruppiert.
2. Ein Vektor entspricht einer Matrixzeile mit `dimension * 4` Bytes.
3. Für ausreichend große Blöcke werden verglichen:
   - roh,
   - DEFLATE,
   - SQAR-Spaltentraversierung + DEFLATE mit den Prädiktoren `none`, `top`, `xor2d`, `paeth`.
4. Nur die kleinste Variante wird gespeichert.
5. `min_savings_pct` verhindert Kompression, die den CPU-/Format-Aufwand nicht ausreichend verdient.
6. Leser können Blöcke anderer Vektordimensionen überspringen, ohne sie zu dekomprimieren.

Der vollständige SQAR-Detector/Recursive-Search wurde bewusst **nicht** übernommen. Für NeuroForge ist die Vektordimension bereits bekannt und liefert die relevante 2D-Geometrie ohne teure Width-/Boundary-Suche.

## Rückwärtskompatibilität

`NFVJ1` bleibt lesbar. Beim Öffnen wird ein V1-Journal best-effort in eine temporäre V2-Datei konvertiert und anschließend atomar ersetzt. Schlägt diese optionale Konvertierung fehl, bleibt V1 aktiv und unverändert.

Das Vector Journal ist weiterhin kein Durability-Anker; die autoritativen Daten bleiben Memory-Segmente + WAL/Checkpoint.

## Default-Konfiguration

```json
{
  "storage": {
    "vector_journal": {
      "compression": "sqar-auto",
      "block_vectors": 128,
      "min_block_bytes": 65536,
      "min_savings_pct": 0.01
    }
  }
}
```

`compression` akzeptiert `sqar-auto` oder `off`.

## Probe-Ergebnisse

Vor der Integration wurden repräsentative NeuroForge-Memory-Records (JSON + Embeddings) mit dem SQAR-PoC getestet. Dort gewann die adaptive SQAR-Suche in den Proben **nicht** gegen normales DEFLATE; deshalb wurde dieser Pfad nicht migriert.

Auf blockweise angeordneten 768-D-Float32-Vektoren zeigte die dimensionsbewusste Variante dagegen Potenzial. In synthetischen Proben lagen die zusätzlichen Einsparungen gegenüber DEFLATE je nach Struktur grob zwischen ~2 % und ~62 %. Der integrierte Round-trip-Test mit einem bewusst strukturierten 64×768-Vektorblock speichert 196,608 Byte Roh-Vektordaten als 69,337 Byte komprimierten Payload (~64.7 % Payload-Ersparnis gegenüber roh).

Diese Zahlen sind **keine Aussage über reale Embedding-Modelle**. Die tatsächliche Wirkung hängt stark von deren Byte-/Dimensionskorrelation ab. Der Codec ist deshalb als Auswahlverfahren implementiert: ungeeignete Daten werden nicht zu einer größeren Darstellung gezwungen.

## Validierung

Ausgeführt auf dem migrierten Quellbaum:

```text
go test ./...                 PASS
go vet ./...                  PASS
go build ./cmd/server ./cmd/worker ./cmd/bench   PASS
go test -race ./internal/store -run TestVectorJournal   PASS
```

Zusätzliche Tests decken ab:

- bitgenauen NFVJ2/SQAR-Round-trip,
- automatische NFVJ1 → NFVJ2-Migration,
- Journal-Statistiken und Auswahl eines tatsächlich kleineren SQAR-Blocks.
