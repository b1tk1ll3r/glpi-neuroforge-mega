# Migration v0.8.0 → v0.8.1

Es ist keine State- oder Segmentmigration erforderlich. Das vorhandene `data/`-Verzeichnis kann direkt weiterverwendet werden.

Neu:

- `POST /api/v1/goals/{id}/pause`
- `POST /api/v1/goals/{id}/resume`
- das bestehende `DELETE /api/v1/goals/{id}` ist nun im Admin-UI erreichbar
- SearXNG-Dateitreffer können über die Dokument-Ingestion gelernt werden

Für PDF-Dateitreffer muss `pdftotext`/Poppler auf dem NeuroForge-Host vorhanden sein.
