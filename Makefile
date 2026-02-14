# refind-btrfs-snapshots Makefile

BINARY    := refind-btrfs-snapshots
MODULE    := github.com/jmylchreest/refind-btrfs-snapshots
VERSION   ?= dev
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GOARCH    ?= $(shell go env GOARCH)
GOOS      ?= linux

LDFLAGS   := -s -w \
  -X '$(MODULE)/cmd.Version=$(VERSION)' \
  -X '$(MODULE)/cmd.Commit=$(COMMIT)' \
  -X '$(MODULE)/cmd.BuildTime=$(DATE)'

.PHONY: build test vet lint clean release tag help

## build: Build the binary for the current platform
build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY) .

## test: Run tests with race detection
test:
	go test -race ./...

## vet: Run go vet
vet:
	go vet ./...

## lint: Run staticcheck
lint:
	staticcheck ./...

## check: Run vet, lint, and tests
check: vet lint test

## coverage: Run tests with coverage report
coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## clean: Remove build artifacts
clean:
	rm -rf dist/ coverage.out coverage.html

## release: Create a release tag (usage: make release VERSION=x.y.z)
release:
	@if [ "$(VERSION)" = "dev" ]; then \
		echo "ERROR: VERSION is required. Usage: make release VERSION=x.y.z"; \
		exit 1; \
	fi
	@if ! echo "$(VERSION)" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "ERROR: VERSION must be semver (x.y.z), got: $(VERSION)"; \
		exit 1; \
	fi
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "ERROR: Working tree is dirty. Commit or stash changes first."; \
		exit 1; \
	fi
	git tag -a "v$(VERSION)" -m "Release v$(VERSION)"
	@echo "Tagged v$(VERSION). Push with: git push origin v$(VERSION)"

## tag: Alias for release
tag: release

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /' | column -t -s ':'
