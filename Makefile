# Haushaltsbuch — Makefile

BINARY  := haushaltsbuch
PKG     := github.com/daknoblo/Haushaltsbuch
CMD     := ./cmd/haushaltsbuch

TAILWIND            := ./bin/tailwindcss
TAILWIND_VERSION    := v3.4.19
GOLANGCI_VERSION    := v2.13.1
GOVULNCHECK_VERSION := v1.1.4
CSS_INPUT           := internal/web/assets/input.css
CSS_OUTPUT          := internal/web/assets/static/app.css

# Mirrors how the release workflow resolves the version, so a local build is
# labelled exactly like the CI build of the same commit.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE   ?= ghcr.io/daknoblo/haushaltsbuch

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

.PHONY: help fmt fmt-check vet lint test build run generate css tailwind tools check verify docker release clean

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'

## fmt: format Go and templ sources
fmt:
	gofmt -w .
	go tool templ fmt internal/web

## fmt-check: fail when sources are not formatted
fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt'ed:"; echo "$$out"; exit 1; fi

## vet: run go vet
vet:
	go vet ./...

# golangci-lint must analyse with the same toolchain the module declares,
# otherwise it fails to parse a newer standard library.
GO_TOOLCHAIN := $(shell awk '/^toolchain /{print $$2}' go.mod)

## lint: run golangci-lint (same version as CI)
lint:
	GOTOOLCHAIN=$(GO_TOOLCHAIN) $(shell go env GOPATH)/bin/golangci-lint run ./...

## test: run the test suite with the race detector
test:
	go test -race ./...

## build: compile a static, CGO-free binary into bin/
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

## run: run the application locally
run:
	go run $(CMD)

## generate: regenerate the templ templates and the Tailwind CSS
generate: css
	go tool templ generate

## css: compile the Tailwind CSS into the embedded static output
css:
	$(TAILWIND) -c tailwind.config.js -i $(CSS_INPUT) -o $(CSS_OUTPUT) --minify

## tailwind: download the standalone Tailwind CLI for this platform (run once)
tailwind:
	@mkdir -p bin
	@os=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
	arch=$$(uname -m); \
	case "$$os-$$arch" in \
	  darwin-arm64) asset=tailwindcss-macos-arm64 ;; \
	  darwin-x86_64) asset=tailwindcss-macos-x64 ;; \
	  linux-aarch64|linux-arm64) asset=tailwindcss-linux-arm64 ;; \
	  linux-x86_64) asset=tailwindcss-linux-x64 ;; \
	  *) echo "unsupported platform $$os-$$arch"; exit 1 ;; \
	esac; \
	curl -fsSL "https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/$$asset" -o $(TAILWIND); \
	chmod +x $(TAILWIND)

## tools: install the pinned developer tooling
tools:
	go get -tool github.com/a-h/templ/cmd/templ@latest
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

## verify: run the plausibility suite on its own, with its findings spelled out
verify:
	go test ./internal/web/ -run TestPlausibility -count=1 -v

## check: run everything CI runs
check: fmt-check vet lint test
	CGO_ENABLED=0 go build ./...

## docker: build the container image
docker:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(IMAGE):$(VERSION) .

## release: tag the next version (usage: make release BUMP=major|minor|patch)
# The tag annotation becomes the body of the GitHub release, so the editor is
# opened deliberately instead of writing a canned message.
release:
	@set -e; \
	case "$(BUMP)" in \
	  major|minor|patch) ;; \
	  *) echo "usage: make release BUMP=major|minor|patch"; exit 1 ;; \
	esac; \
	[ -z "$$(git status --porcelain)" ] || { echo "working tree is dirty"; exit 1; }; \
	last=$$(git tag --list 'v*' | sed 's/^v//' \
	        | grep -E '^[0-9]+\.[0-9]+\.[0-9]+$$' | sort -V | tail -n1); \
	[ -n "$$last" ] || last=0.0.0; \
	next=$$(echo "$$last" | awk -F. -v part="$(BUMP)" ' \
		part=="major" { printf "%d.0.0", $$1+1 } \
		part=="minor" { printf "%d.%d.0", $$1, $$2+1 } \
		part=="patch" { printf "%d.%d.%d", $$1, $$2, $$3+1 }'); \
	echo "v$$last -> v$$next"; \
	if [ -n "$(NOTES)" ]; then git tag -a "v$$next" -F "$(NOTES)"; else git tag -a "v$$next"; fi; \
	echo "tag v$$next created — publish it with: git push origin v$$next"

## clean: remove build artifacts
clean:
	rm -rf bin/ out/
