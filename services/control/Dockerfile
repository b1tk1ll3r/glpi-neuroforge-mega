FROM golang:1.27-alpine AS build
WORKDIR /src
COPY . .
RUN go test ./... && \
    go vet ./... && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/control .
FROM alpine:3.24
RUN addgroup -S app && adduser -S -G app app
COPY --from=build /out/control /usr/local/bin/control
USER app
EXPOSE 8070
ENTRYPOINT ["/usr/local/bin/control"]
