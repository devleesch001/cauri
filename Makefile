VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all ui build test lint clean

all: ui build

ui:
	cd ui && npm ci && npm run build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/cauri ./cmd/cauri

test:
	go test ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf bin
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
