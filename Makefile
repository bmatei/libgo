

.PHONY: all build fmt lint test

all: build fmt lint

build:
	@go build ./...

fmt:
	@go fmt ./...

lint:
	@golangci-lint run ./...

test:
	@go test -count=1 ./...
