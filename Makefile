# Haushaltsbuch — Makefile

BINARY  := haushaltsbuch
PKG     := github.com/daknoblo/Haushaltsbuch
CMD     := ./cmd/haushaltsbuch

TAILWIND         := ./bin/tailwindcss
TAILWIND_VERSION := v3.4.17
CSS_INPUT        := internal/web/assets/input.css
CSS_OUTPUT       := internal/web/assets/static/app.css

VERSION ?= $(shell date -u +v%Y%m%d-%H%M)
CHANNEL ?= local
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE   ?= ghcr.io/daknoblo/haushaltsbuch

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Channel=$(CHANNEL) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

.PHONY: all build run test vet lint tidy generate css tailwind tools docker clean help

## all: regenerate templates, compile CSS and build the binary
all: generate css build

## build: compile a static, CGO-free binary into bin/
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) $(CMD)

## run: run the application locally
run:
	go run $(CMD)

## test: run the test suite with the race detector
test:
	go test -race ./...

## vet: run go vet
vet:
	go vet ./...

## lint: run golangci-lint (same version as CI)
lint:
	$(shell go env GOPATH)/bin/golangci-lint run ./...

## tidy: tidy go.mod / go.sum
tidy:
	go mod tidy

## generate: generate templ templates (*_templ.go)
generate:
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

## tools: install the templ tool dependency
tools:
	go get -tool github.com/a-h/templ/cmd/templ@latest
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2

## docker: build the container image
docker:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg CHANNEL=$(CHANNEL) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(IMAGE):$(VERSION) .

## clean: remove build artifacts
clean:
	rm -rf bin/ out/

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
