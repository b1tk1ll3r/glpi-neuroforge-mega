# Migration NeuroForge v0.3 -> v0.4

## 1. Backup

Vor dem ersten v0.4-Start das komplette v0.3-Datenverzeichnis sichern.

Mindestens:

```text
state.json
secrets.json
wal/
hnsw.snapshot.json
```

## 2. v0.4 mit demselben Datenverzeichnis starten

```bash
./neuroforge -data /srv/neuroforge/data
```

Beim ersten Start:

1. lädt NeuroForge den v0.3-Checkpoint,
2. spielt das v0.3-WAL ein,
3. migriert vollständige Memory-Bodies nach `memory-segments/`,
4. baut bzw. übernimmt den HNSW,
5. schreibt `hnsw-index/base.json` und `manifest.json`,
6. schreibt einen kompakten v0.4-Checkpoint.

Danach enthält `state.json` Memory-Metadaten, aber nicht mehr den großen Text-/Vektor-Body. Daher gehören `memory-segments/` ab v0.4 zwingend zum Backup.

## 3. Cluster ist standardmäßig aus

Die Migration aktiviert keinen Cluster automatisch. Für HA/Replikation zuerst auf allen Nodes ein identisches `NEUROFORGE_CLUSTER_TOKEN` setzen und anschließend `cluster` über das Webinterface konfigurieren.

Für einen 3-Node-Cluster empfiehlt sich zuerst ein statischer Leader und `quorum=2`.

## 4. Alte HNSW-Datei

`hnsw.snapshot.json` kann als Legacy-Fallback noch gelesen werden. Nach erfolgreichem v0.4-Start wird die neue Struktur unter `hnsw-index/` benutzt. Die alte Datei kann nach einem verifizierten Backup entfernt werden.

## 5. Rollback

Ein v0.3-Binary versteht den kompakten v0.4-Checkpoint nicht als vollständigen Memory-State. Ein Rollback sollte deshalb aus dem **vor der Migration erstellten v0.3-Backup** erfolgen, nicht aus einem bereits migrierten v0.4-Datenverzeichnis.
