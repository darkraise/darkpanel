SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := build

# Outside a git checkout (a source tarball) git fails and both fall back.
# Only release tags name a version; the rolling index release's tag does not.
VERSION ?= $(or $(shell git describe --tags --match 'v*' --always --dirty 2>/dev/null),dev)
COMMIT ?= $(or $(shell git rev-parse --short HEAD 2>/dev/null),unknown)
ARCHES := amd64 arm64
BUILDINFO := github.com/darkraise/darkpanel/internal/buildinfo
# ldflags takes the version, so harness-builds can stamp each build its own.
ldflags = -s -w -X $(BUILDINFO).Version=$(1) -X $(BUILDINFO).Commit=$(COMMIT)
LDFLAGS := $(call ldflags,$(VERSION))
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
RELEASE_DIR := bin/release
CYCLONEDX ?= cyclonedx-gomod
GIT_CLIFF ?= git-cliff
# install-sh embeds the production trusted keys; KEYS=harness embeds the
# harness test key and base URL instead.
KEYS ?= production
# The harness's signed test releases (spec section 13), as version:tags.
# Every one trusts only the harness key; the scenarios use no UI, so none
# embeds it.
HARNESS_DIR := bin/harness
HARNESS_BUILDS := 0.2.0:harnesskeys 0.3.0:harnesskeys 0.3.1:harnesskeys,harnessbroken \
	0.3.2:harnesskeys,harnesscrash 0.3.3:harnesskeys 0.3.4:harnesskeys,harnessmigration,harnessbroken
# A string from web/index.html; finding it in a binary proves the UI is embedded.
UI_MARKER := <title>Darkpanel</title>

# ADR 0001: build only with the toolchain go.mod pins, never a downloaded one.
export GOTOOLCHAIN := local

# release is phony on purpose: the release/ directory would otherwise satisfy it.
.PHONY: web-deps web build verify-build lint-go lint-web lint-sh lint test-go test-web test-adr test-release test schemas vuln check clean release-check release release-prep install-sh harness-builds fuzz

# The stamp reruns the install only when the manifest, the lockfile or the
# install policy changes.
WEB_STAMP := web/node_modules/.stamp

web-deps: $(WEB_STAMP)

$(WEB_STAMP): web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml
	pnpm -C web install --frozen-lockfile
	touch $@

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
	DARKPANEL_REQUIRE_UI=1 env -u GOOS -u GOARCH go test -count=1 ./web/
	DARKPANEL_REQUIRE_UI=1 env -u GOOS -u GOARCH go test -count=1 -run '^(TestEndToEnd|TestFleetEndToEnd)$$' ./internal/controller/server/
	hostos=$$(go env GOHOSTOS); hostarch=$$(go env GOHOSTARCH); \
	if [ "$$hostos" = linux ] && [[ " $(ARCHES) " == *" $$hostarch "* ]]; then \
		bin/darkpanel-linux-$$hostarch version --json | grep -qF '"commit":"$(COMMIT)"'; \
	else \
		echo "verify-build: skipped the version run: a $$hostos/$$hostarch host cannot run the linux binaries"; \
	fi

lint-go:
	golangci-lint run
	go vet ./...

lint-web: web-deps
	pnpm -C web lint
	pnpm -C web typecheck

lint-sh:
	shellcheck release/*.sh $(wildcard install.sh) internal/modules/firewall/testdata/capture.sh \
		internal/modules/firewall/eval/testdata/docker28/capture.sh internal/platform/netfilter/testdata/ufw/capture.sh

lint: lint-go lint-web lint-sh

test-go:
	go test ./...

test-web: web-deps
	pnpm -C web test

test-adr:
	bash scripts/check-adrs.test.sh
	bash scripts/check-adrs.sh --complete

test-release:
	bash release/snapshots.test.sh
	bash release/package.test.sh
	bash release/sign.test.sh

test: test-go test-web test-adr test-release

schemas:
	go run ./internal/core/schema/schemagen -out schemas

vuln:
	go run $(GOVULNCHECK) ./...

# fuzz runs the fuzz targets that scripts/fuzz.sh lists, each for its budget.
fuzz:
	@test -f scripts/fuzz.sh || { echo "fuzz: scripts/fuzz.sh is missing; it lists the fuzz targets to run" >&2; exit 1; }
	bash scripts/fuzz.sh

check: lint test build verify-build vuln

clean:
	rm -rf bin
	find web/dist -mindepth 1 ! -name .gitkeep -delete

release-check:
	go run ./release/releasetool check-version $(VERSION)

# make release VERSION=x.y.z writes the tarballs, the SBOM, install.sh and
# checksums.txt to bin/release. One SBOM serves both architectures: the module
# graph is the same for every GOARCH of this pure-Go build.
release: release-check verify-build
	rm -rf $(RELEASE_DIR)
	mkdir -p $(RELEASE_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(CYCLONEDX) app -json -licenses -main cmd/darkpanel \
		-output $(RELEASE_DIR)/darkpanel_$(VERSION).cdx.json .
	bash release/package.sh $(VERSION) bin $(RELEASE_DIR)

# make release-prep VERSION=x.y.z freezes the schema snapshots and rewrites
# CHANGELOG.md; the owner commits both, then pushes a signed tag vx.y.z.
# Released snapshots are immutable, so git-cliff is checked before they are frozen.
release-prep: release-check
	@command -v $(GIT_CLIFF) >/dev/null || { echo "release-prep: $(GIT_CLIFF) not found; install git-cliff 2.14.2 or set GIT_CLIFF" >&2; exit 1; }
	bash release/snapshots.sh copy v$(VERSION)
	$(GIT_CLIFF) --tag v$(VERSION) -o CHANGELOG.md

install-sh:
	go run $(if $(filter harness,$(KEYS)),-tags harnesskeys,) ./scripts/gen-install-sh -out install.sh

# harness-builds writes bin/harness/darkpanel-<version> for every harness
# release and the install.sh that trusts the harness key; the harness test's
# -m2 flag names the directory.
harness-builds:
	mkdir -p $(HARNESS_DIR)
	for build in $(HARNESS_BUILDS); do \
		version=$${build%%:*}; \
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -tags "$${build#*:}" \
			-ldflags "$(call ldflags,$$version)" -o $(HARNESS_DIR)/darkpanel-$$version ./cmd/darkpanel; \
	done
	go run -tags harnesskeys ./scripts/gen-install-sh -out $(HARNESS_DIR)/install.sh
