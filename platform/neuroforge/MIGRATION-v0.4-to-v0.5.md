# Migration NeuroForge v0.4 -> v0.5

v0.5 kann ein bestehendes v0.4-Datenverzeichnis direkt öffnen. Die Migration ist so ausgelegt, dass der bisherige statische Cluster-Modus und die vorhandenen Memory-/HNSW-Segmente weiter funktionieren.

## Vorher sichern

Vor dem ersten v0.5-Start den gesamten Datenordner sichern, insbesondere:

```text
state.json
secrets.json
wal/
memory-segments/
hnsw-index/
cluster/
```

Ab v0.4/v0.5 ist `memory-segments/` Teil der Source of Truth und darf nicht weggelassen werden.

## Was beim ersten Start passiert

1. v0.5 liest den vorhandenen v0.4-State.
2. Der Memory-Segment-Scanner ermittelt pro ID den neuesten Record/Tombstone und baut daraus den kompakten In-Memory-Katalog.
3. Neuere WAL-Ereignisse werden danach abgespielt.
4. Bestehende HNSW Base-/Delta-Snapshots werden verwendet, wenn ihre Revision exakt zum Store passt; andernfalls wird der Index sicher rekonstruiert.
5. Beim nächsten Checkpoint schreibt v0.5 `memory_catalog` in `state.json` und lässt die bisherige O(N)-`memories`-Map weg.
6. Neue Cluster-Logsegmente entstehen unter `cluster/log/` sobald Cluster-Prepare/Commit-Einträge geschrieben werden.

## Neue Konfiguration

Bestehende Konfigurationen erhalten Defaults für:

```json
"storage": {
  "index_segments": {
    "background_merge_minutes": 10,
    "merge_at_deltas": 8
  },
  "page_cache": {
    "enabled": true,
    "max_bytes": 268435456
  },
  "tiering": {
    "enabled": true,
    "hot_max_bytes": 536870912,
    "hot_age_minutes": 60,
    "interval_minutes": 5
  }
},
"cluster": {
  "auto_election": false,
  "election_min_ms": 1200,
  "election_max_ms": 2400,
  "heartbeat_ms": 350,
  "log_segment_bytes": 67108864
}
```

`auto_election=false` ist absichtlich der Upgrade-Default. Ein vorhandener v0.4-Cluster arbeitet damit zunächst weiter mit seinem statischen `leader_id`.

## Auto-Election aktivieren

Erst aktivieren, nachdem auf **jedem** Node dieselbe Voting-Membership vollständig konfiguriert ist. Jeder Node listet alle anderen Voting-Nodes als Peers.

Beispiel für drei Nodes:

```json
"cluster": {
  "enabled": true,
  "node_id": "a",
  "leader_id": "",
  "quorum": 0,
  "request_timeout_seconds": 5,
  "auto_election": true,
  "election_min_ms": 1200,
  "election_max_ms": 2400,
  "heartbeat_ms": 350,
  "log_segment_bytes": 67108864,
  "peers": [
    {"id":"b","base_url":"http://node-b:8080","enabled":true,"voting":true},
    {"id":"c","base_url":"http://node-c:8080","enabled":true,"voting":true}
  ]
}
```

`quorum=0` berechnet die Mehrheit automatisch. Das Cluster-Token muss auf allen Nodes identisch sein.

## Semantische Änderung von state.json

In v0.4 enthielt ein kompakter Checkpoint weiterhin eine Memory-Metadaten-Map. In v0.5 ist bei aktivierten Segmenten auch diese Map nicht mehr nötig. `state.json` enthält stattdessen z. B.:

```json
"memory_catalog": {
  "segment_backed": true,
  "count": 1000000,
  "revision": 12345
}
```

Das verkleinert Checkpoints massiv, bedeutet aber: `state.json` ohne `memory-segments/` ist kein vollständiges Backup.

## Rollback

Nach einem v0.5-Checkpoint sollte für ein sauberes Rollback das vorherige v0.4-Backup verwendet werden. Ein v0.4-Binary erwartet die frühere State-Repräsentation und ist nicht dafür ausgelegt, einen v0.5-katalogfreien Checkpoint als vollständigen Memory-State zu interpretieren.

## Kontrolle nach der Migration

```bash
curl http://localhost:8080/admin/api/storage \
  -H 'X-Admin-Token: <ADMIN_TOKEN>'

curl http://localhost:8080/admin/api/cluster \
  -H 'X-Admin-Token: <ADMIN_TOKEN>'
```

Prüfen:

- Memory-Count plausibel
- Segment-Count/Bytes plausibel
- HNSW-Node-Count entspricht erwarteten indexierbaren Memories
- Page-Cache-Limit korrekt
- Cluster-Rolle/Term plausibel
- bei aktiviertem Cluster sind Logsegmente/Entscheidungen sichtbar

## Bekannte Grenze

Die neue Election ist Raft-artig, aber kein vollständiges Raft-Protokoll mit Log-Matching und dynamischem Membership-Consensus. Änderungen an der Voting-Membership daher weiterhin kontrolliert und konsistent auf allen Nodes durchführen.
