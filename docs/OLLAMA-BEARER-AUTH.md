# Ollama Bearer authentication

v1.6.2 can call Ollama-compatible endpoints protected by an HTTP Bearer token.

## Shared configuration

```env
OLLAMA_API_KEY=CHANGE_ME
```

Every supported Ollama request then carries:

```http
Authorization: Bearer CHANGE_ME
```

This applies to health/model discovery (`/api/tags`), chat (`/api/chat`) and embeddings (`/api/embed`).

## NeuroForge overrides

```env
NEUROFORGE_OLLAMA_API_KEY=
NEUROFORGE_WORKER_OLLAMA_API_KEY=
```

`NEUROFORGE_OLLAMA_API_KEY` overrides the shared key for the NeuroForge Master/provider. `NEUROFORGE_WORKER_OLLAMA_API_KEY` overrides it for model-capable NeuroForge workers. If the override is empty, workers fall back to `OLLAMA_API_KEY`; the Master also accepts `OLLAMA_API_KEY` as a shared alias when no NeuroForge-specific key is supplied.

## Agent pools

The GLPI Agent uses one `OLLAMA_API_KEY` for all URLs in `OLLAMA_URLS`. This is intentional for a pool behind one common authentication boundary. Deploy separate Agent instances or a common gateway if individual nodes require unrelated credentials.

## Native Ollama

The environment variable configures the **clients**, not the bundled native Ollama server. Native Ollama does not gain access control from this setting alone. To require authentication, place Ollama behind an authentication-capable reverse proxy/gateway and point `OLLAMA_URL`, `OLLAMA_URLS` or `OLLAMA_BASE_URL` at that endpoint.

Do not put credentials into Ollama URLs. Keep URLs credential-free and use the Bearer variable.
