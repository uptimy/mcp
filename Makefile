.PHONY: build test lint fmt run

build:
	go build -o bin/mcp ./cmd/mcp

test:
	go test -race ./...

lint:
	go vet ./...

fmt:
	gofmt -w .

# Runs against production services; authenticate with your own upt_ key.
run:
	go run ./cmd/mcp
