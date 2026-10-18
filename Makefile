SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := build

VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT ?= $(shell git rev-parse --short HEAD)
ARCHES := amd64 arm64
BUILDINFO := github.com/darkraise/darkpanel/internal/buildinfo
LDFLAGS := -s -w -X $(BUILDINFO).Version=$(VERSION) -X $(BUILDINFO).Commit=$(COMMIT)
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
# A string from web/index.html; finding it in a binary proves the UI is embedded.
UI_MARKER := <title>Darkpanel</title>

# ADR 0001: build only with the toolchain go.mod pins, never a downloaded one.
export GOTOOLCHAIN := local

.PHONY: web-deps web build verify-build lint-go lint-web lint test-go test-web test-adr test schemas vuln check clean

web-deps:
	pnpm -C web install --frozen-lockfile

web: web-deps
	pnpm -C web build

build: web
	for arch in $(ARCHES); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o bin/darkpanel-linux-$$arch ./cmd/darkpanel; \
	done

verify-build: build
	file bin/darkpanel-linux-amd64 | grep -F 'ELF 64-bit LSB executable, x86-64' | grep -qF 'statically linked'
	file bin/darkpanel-linux-arm64 | grep -F 'ELF 64-bit LSB executable, ARM aarch64' | grep -qF 'statically linked'
	for arch in $(ARCHES); do grep -qaF '$(UI_MARKER)' bin/darkpanel-linux-$$arch; done
	DARKPANEL_REQUIRE_UI=1 go test -count=1 ./web/
	DARKPANEL_REQUIRE_UI=1 go test -count=1 -run '^TestEndToEnd$$' ./internal/controller/server/
	bin/darkpanel-linux-$$(go env GOARCH) version --json | grep -qF '"commit":"$(COMMIT)"'

lint-go:
	golangci-lint run
	go vet ./...

lint-web: web-deps
	pnpm -C web lint
	pnpm -C web typecheck

lint: lint-go lint-web

test-go:
	go test ./...

test-web: web-deps
	pnpm -C web test

test-adr:
	bash scripts/check-adrs.test.sh
	bash scripts/check-adrs.sh --complete

test: test-go test-web test-adr

schemas:
	go run ./internal/core/schema/schemagen -out schemas

vuln:
	go run $(GOVULNCHECK) ./...

check: lint test build verify-build vuln

clean:
	rm -rf bin
	find web/dist -mindepth 1 ! -name .gitkeep -delete
