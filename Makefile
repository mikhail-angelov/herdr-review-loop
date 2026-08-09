.PHONY: build test tidy install-plugin release

build:
	bash bin/ensure-binary.sh --build

test:
	go test ./...

tidy:
	go mod tidy

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
