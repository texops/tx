.PHONY: fmt lint test build

fmt:
	go fmt ./...

lint:
	golangci-lint run ./...

test:
	go test ./...

build:
	go build -o bin/tx ./cmd/tx
