# Migration v1.1.0 → v1.2.0

## 1. Neue Secrets übernehmen

```bash
./scripts/generate-secrets.sh
```

Zusätzlich wird `SEARXNG_SECRET` ausgegeben. SearXNG ist optional; der Secret wird erst für das `research`-Profil benötigt.

## 2. Controlled Learning prüfen

Empfohlen:

```text
NEUROFORGE_CONTROLLED_LEARNING=true
OUTCOME_LEARNING_ENABLED=true
OUTCOME_LEARNING_FAIL_OPEN=false
```

Bestehende NeuroForge-Daten werden nicht gelöscht. Der Modus ändert, welche neuen Signale automatisch gelernt werden.

## 3. Human Outcome Flow verwenden

Neue Agent-Runs speichern den für Learning benötigten Ticket-/Reply-Snapshot. Alte Runs aus v1.1.0 können deshalb bewusst nicht nachträglich als validiertes Outcome gelernt werden, wenn dieser Snapshot fehlt.

Im Agent-Dashboard den Run öffnen und **KI-Antwort bestätigen** bzw. **KI-Antwort korrigieren** verwenden.

## 4. Research optional starten

```bash
./scripts/research-up.sh
```

Das startet das Compose-Profil `research` und aktiviert SearXNG/Research für den NeuroForge-Start. Zyklische Autonomie bleibt separat deaktiviert, solange `NEUROFORGE_AUTONOMY_ENABLED=false` ist.

## 5. Rollback

- SearXNG stoppen: `docker compose --profile research stop searxng`
- Research deaktivieren: `NEUROFORGE_RESEARCH_ENABLED=false`, `NEUROFORGE_SEARXNG_ENABLED=false`
- Autonomy deaktivieren: `NEUROFORGE_AUTONOMY_ENABLED=false`
- Outcome Learning deaktivieren: `OUTCOME_LEARNING_ENABLED=false`

Das lokale Outcome-Audit und bereits gelernte Memories werden dadurch nicht gelöscht.
