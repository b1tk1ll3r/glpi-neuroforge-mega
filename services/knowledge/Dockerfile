FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN go test ./... && \
    go vet ./... && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/kb-helpdesk ./cmd/server

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/kb-helpdesk /usr/local/bin/kb-helpdesk
ENV APP_MODE=editor \
    DATA_DIR=/data/knowledge \
    BACKUP_DIR=/data/backups \
    STAGING_DIR=/data/staging \
    AI_FALLBACK_ENABLED=false \
    OLLAMA_TIMEOUT=10m \
    LISTEN_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/kb-helpdesk"]
