

.PHONY: all build fmt lint

all: build fmt lint

build:
	@go build ./...

fmt:
	@go fmt ./...

lint:
	@golangci-lint run ./...
