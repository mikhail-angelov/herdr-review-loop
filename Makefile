.PHONY: build test tidy install-plugin

build:
	bash bin/ensure-binary.sh --build

test:
	go test ./...

tidy:
	go mod tidy

install-plugin: build
	herdr plugin link .
