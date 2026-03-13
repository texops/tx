.PHONY: fmt lint test build

fmt:
	go fmt ./...

lint:
	./scripts/install-golangci-lint.sh
	./bin/golangci-lint run ./...

test:
	go test ./...

build:
	go build -o bin/tx ./cmd/tx
