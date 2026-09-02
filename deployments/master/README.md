# Master Deployment

The Master compose deliberately contains **no local CPU/GPU NeuroForge workers**. It uses remote subagents. Agent, Knowledge and NeuroForge use `OLLAMA_BASE_URL`/`OLLAMA_URLS`, normally pointing to the GPU subagent host.

Before first start:
- replace the `192.0.2.x` example addresses;
- fill the five GLPI credentials in `.env`;
- expose the NeuroForge Master port only to trusted subagent networks or put it behind TLS/mTLS/reverse proxy;
- keep Grafana and Prometheus bound to loopback unless intentionally proxied.

Start:
```bash
./preflight.sh
docker compose --profile research --profile monitoring up -d
```

Monitoring:
- Prometheus: `http://127.0.0.1:${PROMETHEUS_HOST_PORT:-9090}`
- Grafana: `http://127.0.0.1:${GRAFANA_HOST_PORT:-3000}`
- Dashboard is provisioned automatically in folder `NeuroForge`.
