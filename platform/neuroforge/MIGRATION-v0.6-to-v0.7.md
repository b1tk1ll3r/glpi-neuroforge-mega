# Migration v0.6-dev → v0.7.0

1. Vollständiges Backup des bisherigen `data/`-Verzeichnisses erstellen.
2. v0.7-Binary/Source installieren und dasselbe Datenverzeichnis öffnen.
3. Beim Start werden fehlende Learning-Policy-/HTTP-/Security-Defaults ergänzt.
4. Legacy-Memories bleiben unverändert; fehlende Herkunft wird als `legacy/unknown` angezeigt.
5. Neue Learning Events werden ab v0.7 persistent erfasst; historische Events können nicht rückwirkend rekonstruiert werden.
6. Admin-Secrets sind im UI jetzt standardmäßig maskiert. Bestehende Secrets bleiben erhalten.
7. Prüfe `/readyz`, „Modelle & Routing“ und danach „Wissen & Lernen“.
8. Passe die Learning Policy vor aktivierter Goal-Autonomie an.

Memory-Segment-, WAL-, HNSW- und Disk-PQ-Formate werden durch diese Explainability-/Policy-Erweiterung nicht absichtlich gebrochen. Vor einem Rollback trotzdem immer mit einer Kopie des Datenverzeichnisses arbeiten.
