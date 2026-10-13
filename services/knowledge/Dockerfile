FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN go test ./... && \
    go vet ./... && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/kb-helpdesk ./cmd/server

FROM alpine:3.24
# Fixed non-root UID/GID 65532, same as the Agent runtime. The bind-mounted data
# directories are handed over by the one-shot knowledge-data-init compose
# service, which runs this image as root with /usr/local/bin/kb-data-init.
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 65532 kb && \
    adduser -S -D -H -u 65532 -G kb -s /sbin/nologin kb
COPY --from=build /out/kb-helpdesk /usr/local/bin/kb-helpdesk
COPY data-init.sh /usr/local/bin/kb-data-init
# Strip CR in case the build context was checked out with CRLF (Windows).
RUN sed -i 's/\r$//' /usr/local/bin/kb-data-init && chmod 0755 /usr/local/bin/kb-data-init
ENV APP_MODE=editor \
    DATA_DIR=/data/knowledge \
    BACKUP_DIR=/data/backups \
    STAGING_DIR=/data/staging \
    AI_FALLBACK_ENABLED=false \
    OLLAMA_TIMEOUT=10m \
    LISTEN_ADDR=:8080
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/kb-helpdesk"]
