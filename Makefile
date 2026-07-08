# Haushaltsbuch — Makefile

BINARY  := haushaltsbuch
PKG     := github.com/daknoblo/Haushaltsbuch
CMD     := ./cmd/haushaltsbuch

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

.PHONY: build run test vet tidy generate tools docker clean help

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

## tidy: tidy go.mod / go.sum
tidy:
	go mod tidy

## generate: generate templ templates (*_templ.go)
generate:
	go tool templ generate

## tools: install the templ tool dependency
tools:
	go get -tool github.com/a-h/templ/cmd/templ@latest

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
