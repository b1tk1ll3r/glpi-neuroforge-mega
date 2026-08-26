# Changelog

## Staging Review Workflow – 2026-07-29

- Editor now has **Produktiv / Staging** scopes.
- Staging drafts can be searched, filtered, opened and edited with the existing form/Raw JSON editor.
- Added single and bulk **Freigeben → Produktiv**.
- Added single and bulk delete with safe archive under `staging/.trash`.
- Promoted source drafts are retained under `staging/.approved` for audit purposes.
- Promotion refuses duplicate production IDs or target files.
- Dual Compose now mounts the same staging directory read/write into the editor container.
- Google/viewer mode remains read-only for all review actions.
