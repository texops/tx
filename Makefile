.PHONY: fmt lint test build man-text

fmt:
	./scripts/make-fmt.sh

lint:
	./scripts/install-golangci-lint.sh
	./bin/golangci-lint run ./...

test:
	go test ./...

build:
	go build -o bin/tx ./cmd/tx

man-text:
	mandoc -T utf8 man/tx.1 | col -bx > man/tx.1.txt
