# Hotfix: Poll-Diagnose und manuelle Neuanalyse

Dieser Hotfix behebt nicht den Poller selbst, sondern die fehlende Sichtbarkeit seines Ergebnisses. Der Agent filtert unveränderte, bereits verarbeitete Ticketversionen bereits vor der Queue. Dadurch konnten Dashboard, Queue und Audit leer aussehen, obwohl der GLPI-Poll korrekt lief.

## Neue Diagnosewerte

`GET /api/status` liefert zusätzlich:

- `polls_total`
- `last_poll`
- `poll_last_fetched`
- `poll_last_seen`
- `poll_last_unseen`
- `poll_last_enqueued`
- `poll_last_rejected`
- `poll_last_error`
- `processed_version_count`

Der erste erfolgreiche Poll wird außerdem einmalig auf INFO-Level protokolliert. Weitere Polls erscheinen auf DEBUG-Level.

## Manuelle Neuanalyse

Im Dashboard kann eine Ticket-ID manuell neu analysiert werden. Der Lauf erhält den Trigger `manual_recheck` und umgeht die Versions-Deduplizierung genau für diesen Lauf. Der gespeicherte Betriebszustand wird nicht gelöscht.

Im LIVE-Modus gelten weiterhin alle konfigurierten Auto-Aktionen. Vor der manuellen Neuanalyse sollte daher geprüft werden, ob automatische Kategorie-, Prioritäts- oder Antwortaktionen aktiv sind.
