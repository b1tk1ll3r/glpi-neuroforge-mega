.PHONY: build test run run-google docker clean

build:
	go build -o kb-helpdesk ./cmd/server

test:
	go test ./...

run:
	APP_MODE=editor go run ./cmd/server -data ./knowledge

run-google:
	APP_MODE=google go run ./cmd/server -data ./knowledge

docker:
	docker compose up --build

clean:
	rm -f kb-helpdesk kb-editor
