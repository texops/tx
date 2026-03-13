.PHONY: fmt test build

fmt:
	go fmt ./...

test:
	go test ./...

build:
	go build -o bin/tx ./cmd/tx
