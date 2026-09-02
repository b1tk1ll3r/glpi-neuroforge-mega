# Prometheus / Grafana Monitoring

The `monitoring` profile starts:
- Prometheus 3.14.0
- Grafana 13.2.0
- node-exporter 1.12.1 on the Master

Prometheus also scrapes the CPU/GPU subagent node-exporters and NVIDIA DCGM exporter using targets from the Master `.env`. NeuroForge `/metrics` is authenticated with the metrics bearer token; an init container writes that token into a Docker volume instead of embedding it in the Prometheus config.

Provisioned dashboard panels include:
- Master up, memories, synapses
- online CPU/GPU workers
- durable jobs by status
- inflight/capacity by worker
- linked/isolated/multi-linked memories
- graph degree/components/largest component
- HNSW and Disk-PQ population
- HTTP request rate and P95 latency
- Master heap
- Master/CPU/GPU host CPU and memory
- NVIDIA GPU utilization, framebuffer and power (when DCGM exporter is enabled)

The bundled Prometheus alert rules cover Master down, missing CPU/GPU workers, failed jobs, stuck `apply_wait`, large queue, and a persistently highly-isolated graph.

Validation:
```bash
./validate.sh
```
This renders the Prometheus template with the targets from the Master `.env` and runs the pinned Prometheus image's own `promtool check config`.
