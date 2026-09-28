# dirserve — build, test, package. Stdlib only; no network needed after checkout.

BINARY  := dirserve
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X dirserve/internal/server.version=$(VERSION)
GOFLAGS := -trimpath
IMAGE   ?= dirserve

# POSIX make needs explicit recipe prefixes.
SHELL := /bin/sh

.DEFAULT_GOAL := build

PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64

.PHONY: build test vet fmt fmt-check e2e release image clean run

## build: compile the single static binary to ./dirserve
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/dirserve

## release: cross-compile static binaries for the release platforms into ./dist
## (kept in step with the ci.yml `release` job, which attaches them to tags)
release:
	mkdir -p dist
	for platform in $(PLATFORMS); do \
		os=$${platform%%-*}; arch=$${platform##*-}; \
		out=dist/$(BINARY)-$${platform}; \
		if [ "$$os" = "windows" ]; then out=$${out}.exe; fi; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) \
			-ldflags '$(LDFLAGS)' -o $$out ./cmd/dirserve || exit 1; \
	done
	sha256sum dist/$(BINARY)-* > dist/SHA256SUMS

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
	rm -rf dist
