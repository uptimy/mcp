.PHONY: build test lint fmt run

build:
	go build -o bin/mcp ./cmd/mcp

test:
	go test -race ./...

## lint: go vet and golangci-lint (https://golangci-lint.run), as in CI
lint:
	go vet ./...
	golangci-lint run ./...

fmt:
	gofmt -w .

# Runs against production services; authenticate with your own upt_ key.
run:
	go run ./cmd/mcp
