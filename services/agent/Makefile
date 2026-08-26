.PHONY: build dist test race vet fmt check run zip

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o glpi-ai-agent ./cmd/agent

dist:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/glpi-ai-agent-linux-amd64 ./cmd/agent
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/glpi-ai-agent-windows-amd64.exe ./cmd/agent
	cd dist && sha256sum glpi-ai-agent-linux-amd64 glpi-ai-agent-windows-amd64.exe > SHA256SUMS.txt

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

check: fmt test vet race build

run:
	go run ./cmd/agent

zip:
	cd .. && zip -qr glpi-ai-agent.zip glpi-ai-agent \
		-x 'glpi-ai-agent/.env' 'glpi-ai-agent/.env_local' 'glpi-ai-agent/data/*' 'glpi-ai-agent/*.zip' 'glpi-ai-agent/agent' 'glpi-ai-agent/glpi-ai-agent'
