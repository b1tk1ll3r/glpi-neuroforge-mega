FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN go test ./... && \
    go vet ./... && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/neuroforge ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/neuroforge-worker ./cmd/worker && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/neuroforge-bench ./cmd/bench

FROM alpine:3.21 AS server
RUN apk add --no-cache ca-certificates poppler-utils && \
    addgroup -S neuroforge && adduser -S -G neuroforge neuroforge && \
    mkdir -p /app/data && chown -R neuroforge:neuroforge /app
WORKDIR /app
COPY --from=build /out/neuroforge /usr/local/bin/neuroforge
USER neuroforge
VOLUME ["/app/data"]
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=4 CMD wget -q -O - http://127.0.0.1:8080/readyz >/dev/null || exit 1
ENTRYPOINT ["neuroforge", "-data", "/app/data", "-listen", ":8080"]

FROM alpine:3.21 AS worker
RUN apk add --no-cache ca-certificates && addgroup -S neuroforge && adduser -S -G neuroforge neuroforge
COPY --from=build /out/neuroforge-worker /usr/local/bin/neuroforge-worker
USER neuroforge
ENTRYPOINT ["neuroforge-worker"]
