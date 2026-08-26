# Migration v0.8.1 → v0.8.2

Keine Datenmigration ist erforderlich. Das bestehende `data/` kann direkt weiterverwendet werden.

Neu persistiert v0.8.2 bounded `research_runs` im normalen State/WAL-Checkpoint. Bestehende Goals und Learning Cycles bleiben kompatibel; ältere Cycles besitzen schlicht kein `research_run_id`.

Empfohlen:

1. vollständiges Backup des v0.8.1-`data/`-Verzeichnisses,
2. v0.8.2-Binary/Source einspielen,
3. Server mit demselben `-data` starten,
4. `/readyz` prüfen,
5. unter **Ziele & Autonomie → Live Research** einen manuellen Goal-Cycle starten.

Ein während eines Servercrashs noch als `running` persistierter Research-Run wird beim Neustart als `interrupted` markiert. Bereits gelernte Sources/Memories bleiben davon unberührt.
