# Migration v0.7.2 → v0.7.3

No data migration is required. The memory/WAL/segment/PQ formats are unchanged.

## Required configuration review

Existing Ollama nodes automatically remain valid. New runtime fields are optional:

```json
{
  "request_timeout_seconds": 0,
  "num_ctx": 8192,
  "num_predict": 0,
  "think": "off",
  "chat_keep_alive": "30m",
  "embedding_keep_alive": "5m"
}
```

`request_timeout_seconds: 0` means NeuroForge does not add an inference deadline. The request can still be cancelled by the caller or by process shutdown.

To also remove the inbound HTTP response deadline for long browser/API calls, set:

```json
{"http":{"write_timeout_seconds":0}}
```

The existing v0.7.2 value (for example 660) is not overwritten automatically because it may have been an intentional operator choice.

## Behavioral change

Ollama chat requests now receive a finite `num_predict` whenever NeuroForge has a caller/global output limit. If the per-node `num_predict` is 0, that caller/global limit is inherited. This prevents a no-timeout request from also becoming an unbounded-generation request.

## Reverse proxies

If Nginx, Traefik, Caddy, an ingress controller, or a load balancer sits in front of NeuroForge, its own request/response timeout can still terminate long calls. Configure that layer separately.
