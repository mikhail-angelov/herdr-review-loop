# version is the plugin manifest's, not git's: herdr matches the binary against it, and
# bin/ensure-binary.sh refuses a binary whose `version` output disagrees.
VERSION=$(shell awk -F '"' '$$1 ~ /^version[[:space:]]*=/ { print $$2; exit }' herdr-plugin.toml)

.PHONY: all build test lint fmt vet prep tidy version install-plugin release

all: prep build

build:
	bash bin/ensure-binary.sh --build

test:
	go clean -testcache
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
	last_tag=""; \
	while IFS= read -r tag; do \
		if [[ "$$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$$ ]]; then last_tag="$$tag"; break; fi; \
	done < <(git tag --sort=-version:refname); \
	if [[ -z "$$last_tag" ]]; then \
		echo "No vMAJOR.MINOR.PATCH tag found" >&2; \
		exit 1; \
	fi; \
	version="$${last_tag#v}"; \
	patch="$${version##*.}"; \
	next_tag="v$${version%.*}.$$((10#$$patch + 1))"; \
	echo "Releasing $$next_tag"; \
	git tag -a "$$next_tag" -m "Release $$next_tag"; \
	git push origin HEAD; \
	git push origin "$$next_tag"
