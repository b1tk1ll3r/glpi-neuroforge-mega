FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN go test ./... && \
    go vet ./... && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/glpi-ai-agent ./cmd/agent

# One-shot helper used by docker compose to prepare the persistent volume for
# the distroless non-root runtime user (UID/GID 65532).
FROM golang:1.26-alpine AS data-init
ENTRYPOINT ["sh", "-c", "mkdir -p /app/data && chown -R 65532:65532 /app/data && chmod 0750 /app/data"]

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/glpi-ai-agent /app/glpi-ai-agent
COPY knowledge /app/knowledge
VOLUME ["/app/data"]
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=4 CMD ["/app/glpi-ai-agent", "healthcheck"]
USER 65532:65532
ENTRYPOINT ["/app/glpi-ai-agent"]
