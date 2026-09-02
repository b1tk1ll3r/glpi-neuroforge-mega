# CPU Subagent

Runs the durable CPU execution node used primarily for `vector.relink` and graph convergence.

Edit `NEUROFORGE_MASTER_URL`, then:
```bash
./preflight.sh
docker compose --profile monitoring up -d
```

The optional node-exporter listens on port 9100 for Master Prometheus. Restrict it with host firewall rules to the Prometheus/Master host.
