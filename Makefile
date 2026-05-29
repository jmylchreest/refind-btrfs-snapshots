# refind-btrfs-snapshots Makefile

MODULE    := github.com/jmylchreest/refind-btrfs-snapshots
VERSION   ?= dev
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GOARCH    ?= $(shell go env GOARCH)
GOOS      ?= linux

# Binaries this repo builds. Per-binary build targets are derived from this list.
BINARIES  := refind-btrfs-snapshots bls-btrfs-snapshots uki-btrfs-snapshots peseal kernel-spy

# ldflags inject build metadata into internal/version, which every binary that
# wires it (refind, bls) reads from. kernel-spy doesn't import internal/version;
# Go's linker silently ignores -X targets that don't resolve in the final binary,
# so the same ldflags string works for all three.
LDFLAGS   := -s -w \
  -X '$(MODULE)/internal/version.Version=$(VERSION)' \
  -X '$(MODULE)/internal/version.Commit=$(COMMIT)' \
  -X '$(MODULE)/internal/version.BuildTime=$(DATE)'

MAN_DIR   := docs/man

.PHONY: build test vet lint check coverage docs docs-clean uki-fixtures clean release tag help

## build: Build all binaries for the current platform into dist/
build:
	@mkdir -p dist
	@for bin in $(BINARIES); do \
	  echo "  -> dist/$$bin"; \
	  CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    go build -ldflags "$(LDFLAGS)" -o dist/$$bin ./cmd/$$bin || exit 1; \
	done

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

## docs: Regenerate committed man pages under docs/man/ from cobra command trees
##
## Uses each binary's hidden gen-docs subcommand, gated behind the `gendocs`
## build tag so the doc-generation transitive deps (go-md2man) never ship in
## release binaries. Run after editing any cobra Use/Short/Long/flag definition.
docs: docs-clean
	@mkdir -p $(MAN_DIR)
	@TMP=$$(mktemp -d) && \
	  go build -tags=gendocs -o $$TMP/refind-btrfs-snapshots ./cmd/refind-btrfs-snapshots && \
	  $$TMP/refind-btrfs-snapshots gen-docs $(MAN_DIR) && \
	  go build -tags=gendocs -o $$TMP/bls-btrfs-snapshots    ./cmd/bls-btrfs-snapshots    && \
	  $$TMP/bls-btrfs-snapshots gen-docs $(MAN_DIR) && \
	  go build -tags=gendocs -o $$TMP/uki-btrfs-snapshots    ./cmd/uki-btrfs-snapshots    && \
	  $$TMP/uki-btrfs-snapshots gen-docs $(MAN_DIR) && \
	  go build -tags=gendocs -o $$TMP/peseal                 ./cmd/peseal                 && \
	  $$TMP/peseal gen-docs $(MAN_DIR) && \
	  rm -rf $$TMP
	@echo "Generated man pages:"
	@ls $(MAN_DIR)/ | sed 's/^/  /'

docs-clean:
	@rm -f $(MAN_DIR)/*.1

## uki-fixtures: Regenerate committed UKI test fixtures under pkg/uki/testdata/
##
## Requires systemd-ukify locally. The underlying script skips with a clear
## message and exits 0 if ukify isn't on PATH, so this target is safe to
## invoke from environments without it — CI doesn't regenerate, it runs
## tests against the committed binaries.
uki-fixtures:
	@./contrib/regen-uki-test-fixtures.sh

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
