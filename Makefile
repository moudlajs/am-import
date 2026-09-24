# VERSION defaults to the nearest git tag; release builds get it from GoReleaser.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test lint fmt check

build:
	go build -ldflags "$(LDFLAGS)" -o bin/am-import ./cmd/am-import

test:
	go test ./... -race -timeout 2m -coverprofile=coverage.out

lint:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	go vet ./...
	golangci-lint run

fmt:
	gofmt -w .

# What CI runs, in one go.
check: lint test build
