# Migration v1.6.1 -> v1.6.2

v1.6.2 is primarily a packaging/deployment consolidation release. It retains the v1.6.1 NeuroForge recovery and persisted-state behavior.

1. Back up the persistent data volumes/directories.
2. Set `IMAGE_TAG=1.6.2` in the selected deployment `.env`.
3. Keep existing `neuroforge-data`, Agent data and Knowledge files; do not delete volumes.
4. Replace deployment examples with the v1.6.2 variants, preserving real secrets locally.
5. Recreate project containers so changed environment/configuration is applied.
6. Check `/livez`, `/readyz`, Agent `/api/status`, worker status, graph convergence and Prometheus alerts.

The distributed roles now live alongside the standalone Agent/Knowledge/Ollama roles in the same canonical repository.
