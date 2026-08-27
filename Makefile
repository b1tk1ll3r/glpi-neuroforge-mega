SHELL := /bin/sh

.PHONY: test vet build dev-up up preflight release-gate research-up down logs status ps engineering-graph engineering-graph-check

test:
	cd platform/neuroforge && go test ./...
	cd services/agent && go test ./...
	cd services/knowledge && go test ./...
	cd services/control && go test ./...

vet:
	cd platform/neuroforge && go vet ./...
	cd services/agent && go vet ./...
	cd services/knowledge && go vet ./...
	cd services/control && go vet ./...

build:
	docker compose -f docker-compose.yml -f docker-compose.dev.yml build

dev-up:
	docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build

preflight:
	./scripts/preflight.sh

release-gate:
	./scripts/release-gate.sh

up:
	./scripts/preflight.sh
	docker compose pull
	docker compose up -d --remove-orphans

research-up:
	./scripts/research-up.sh

down:
	docker compose down

logs:
	docker compose logs -f --tail=200

ps:
	docker compose ps

status:
	./scripts/status.sh

engineering-graph:
	cd services/control && go run ./cmd/engineering-graph -root ../.. -out engineering-graph.json

engineering-graph-check:
	@tmp=$$(mktemp); trap 'rm -f $$tmp' EXIT; \
	cd services/control && go run ./cmd/engineering-graph -root ../.. -out $$tmp >/dev/null && cmp -s engineering-graph.json $$tmp || { echo "engineering-graph.json is stale; run: make engineering-graph"; exit 1; }
