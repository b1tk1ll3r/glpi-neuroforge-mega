# Backup und Restore – v1.5.0

Die Release-Skripte sichern die drei zustandsbehafteten Produktbereiche konsistent während eines kurzen Maintenance-Stopps: NeuroForge `/app/data`, Agent `/app/data` sowie Knowledge-Produktion, Staging und Knowledge-Backups auf dem Host.

## Backup

```sh
./scripts/backup-data.sh /srv/glpi-neuroforge-backups
```

Das Skript führt zuerst den Produktions-Preflight aus, stoppt `neuroforge-worker`, `agent`, `neuroforge` und `knowledge`, kopiert die beiden Named Volumes sowie die drei Knowledge-Verzeichnisse und erzeugt `MANIFEST` plus `SHA256SUMS`. Danach werden die Services wieder gestartet.

## Restore

```sh
./scripts/restore-data.sh /srv/glpi-neuroforge-backups/20260827T071500Z
```

Restore validiert zuerst alle SHA-256-Prüfsummen, stoppt dieselben Services, leert die beiden Daten-Volumes kontrolliert über `busybox:1.36`, stellt alle Datenbereiche wieder her und startet die Services anschließend. `BACKUP_HELPER_IMAGE` kann auf ein intern freigegebenes Helper-Image gesetzt werden.

## Pflichtprüfung nach Restore

Nach jedem Restore müssen `docker compose ps`, NeuroForge `/readyz`, Knowledge `/api/health`, Agent `/readyz` und ein read-only Control-Center-Aufruf geprüft werden. GLPI-Schreibautomation bleibt bis zum erfolgreichen Smoke-Test deaktiviert (`DRY_RUN=true`, `AUTO_REPLY=false`, `AUTO_PRIORITY=false`, `AUTO_ESCALATION=false`).
