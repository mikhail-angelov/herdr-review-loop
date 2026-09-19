# version is the plugin manifest's, not git's: herdr matches the binary against it, and
# bin/ensure-binary.sh refuses a binary whose `version` output disagrees.
VERSION=$(shell awk -F '"' '$$1 ~ /^version[[:space:]]*=/ { print $$2; exit }' herdr-plugin.toml)

.PHONY: all build test lint fmt vet prep tidy version install-plugin release

all: prep build

build:
	bash bin/ensure-binary.sh --build

# no cache-buster: the tests are hermetic (every one of them keeps its state in t.TempDir), so an
# unchanged package has nothing to re-prove and Go's own cache keys on what went into it
test:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	@rm -f coverage.out

lint:
	golangci-lint run

vet:
	go vet ./...

fmt:
	gofmt -s -w $(shell find . -type f -name "*.go" -not -path "./vendor/*")

# run before every commit
prep: fmt vet lint test

tidy:
	go mod tidy

version:
	@echo "$(VERSION)"

install-plugin: build
	herdr plugin link .

release: SHELL := /bin/bash
release:
	@set -euo pipefail; \
	version="$(VERSION)"; \
	if [[ ! "$$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$$ ]]; then \
		echo "herdr-plugin.toml has no MAJOR.MINOR.PATCH version" >&2; \
		exit 1; \
	fi; \
	tag="v$$version"; \
	if git rev-parse -q --verify "refs/tags/$$tag" >/dev/null; then \
		echo "$$tag is already released; bump version in herdr-plugin.toml first" >&2; \
		exit 1; \
	fi; \
	echo "Releasing $$tag"; \
	git tag -a "$$tag" -m "Release $$tag"; \
	git push origin HEAD; \
	git push origin "$$tag"
