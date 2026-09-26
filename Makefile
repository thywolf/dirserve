# dirserve — build, test, package. Stdlib only; no network needed after checkout.

BINARY  := dirserve
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X dirserve/internal/server.version=$(VERSION)
GOFLAGS := -trimpath
IMAGE   ?= dirserve

# POSIX make needs explicit recipe prefixes.
SHELL := /bin/sh

.DEFAULT_GOAL := build

.PHONY: build test vet fmt fmt-check e2e image clean run

## build: compile the single static binary to ./dirserve
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/dirserve

## test: run every unit and integration test
test:
	go test ./...

## vet: run the standard static checks
vet:
	go vet ./...

## fmt: rewrite sources with gofmt
fmt:
	gofmt -w .

## fmt-check: fail if anything is not gofmt-clean
fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi

## e2e: build, serve a throwaway fixture tree, and assert the HTTP contract
e2e: build
	./e2e.sh

## image: build the container image (skipped with a notice when docker is absent)
image:
	@if command -v docker >/dev/null 2>&1; then \
		docker build -t $(IMAGE) .; \
	else \
		echo "docker not found; skipping image build"; \
	fi

## run: serve the current directory on the default address
run: build
	./$(BINARY)

## clean: remove build output
clean:
	rm -f $(BINARY)
