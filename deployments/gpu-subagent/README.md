# GPU Subagent + Ollama

Runs Ollama on the GPU node, pulls the configured chat and embedding models through two idempotent init services, and registers a GPU worker with capabilities `model.chat` and `model.embed`.

Prerequisites: NVIDIA driver, NVIDIA Container Toolkit and working Docker GPU access.

Edit `NEUROFORGE_MASTER_URL`, then:
```bash
./preflight.sh
docker compose --profile monitoring up -d
```

Ollama is intentionally exposed on port 11434 so the Master Agent/Knowledge services can use this GPU node directly. Restrict ports 11434, 9100 and 9400 to the Master/Prometheus host with a firewall.
