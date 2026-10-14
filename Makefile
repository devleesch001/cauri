VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all ui build test lint clean
.PHONY: release release-forgejo release-snapshot release-build
.PHONY: init-dev dev-api dev-ingestor dev-ui dev-down dev-reset

all: ui build

ui:
	cd ui && npm ci && npm run build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/cauri ./cmd/cauri

test:
	go test ./...

lint:
	golangci-lint run ./...

# Local releases with the single .goreleaser.yaml: GITHUB_TOKEN publishes to
# GitHub; GITEA_TOKEN publishes to the Forgejo instance FORGEJO_URL.
FORGEJO_URL ?= https://git.omniumcorp.fr

release:
	goreleaser release --clean

release-forgejo:
	env -u GITHUB_TOKEN GITHUB_SERVER_URL=$(FORGEJO_URL) goreleaser release --clean

release-snapshot:
	goreleaser release --snapshot --clean

release-build:
	goreleaser build --snapshot --clean

clean:
	rm -rf bin dist
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete

# Development environment, see docs/development.md. Everything lives in
# data/ (ignored by git); override the variables on the command line, for
# example "make dev-api DEV_API_PORT=8091".

DEV_API_PORT ?= 8090
DEV_SENSOR   ?= dev
DEV_TOKEN    := data/$(DEV_SENSOR).token
COWRIE_DIR   := data/cowrie
DEV_COMPOSE  := DEV_UID=$$(id -u) DEV_GID=$$(id -g) docker compose -f deploy/compose.dev.yml

# Checks the tools, installs the UI dependencies, prepares and starts the
# local Cowrie, creates the dev token. Safe to run again.
init-dev:
	@missing=0; \
	for tool in go node npm docker; do \
		command -v $$tool >/dev/null || { echo "missing: $$tool (see docs/development.md)"; missing=1; }; \
	done; \
	docker compose version >/dev/null 2>&1 || { echo "missing: docker compose"; missing=1; }; \
	[ $$missing -eq 0 ] || exit 1; \
	command -v golangci-lint >/dev/null || echo "warning: golangci-lint not found, 'make lint' will fail"
	cd ui && npm install
	@# Cowrie does not create these and fails without them.
	mkdir -p $(COWRIE_DIR)/log/cowrie $(COWRIE_DIR)/lib/cowrie/tty $(COWRIE_DIR)/lib/cowrie/downloads
	$(DEV_COMPOSE) up -d cowrie
	@# Keep the token only if the database still knows it (it may have been reset).
	@if [ -s $(DEV_TOKEN) ] && go run ./cmd/cauri token verify --token-file $(DEV_TOKEN); then \
		echo "$(DEV_TOKEN) kept"; \
	else \
		go run ./cmd/cauri token create --sensor $(DEV_SENSOR) > $(DEV_TOKEN).tmp && mv $(DEV_TOKEN).tmp $(DEV_TOKEN); \
		echo "restart make dev-ingestor if it is running: it reads the token at start"; \
	fi
	@echo; echo "Ready. In three terminals: make dev-api, make dev-ingestor, make dev-ui"; \
	echo "then open http://localhost:5173 and connect to Cowrie: ssh -p $${COWRIE_DEV_PORT:-22222} admin@127.0.0.1 (password admin)"

dev-api:
	CAURI_LISTEN=:$(DEV_API_PORT) go run ./cmd/cauri api --log-level debug

dev-ingestor:
	@[ -s $(DEV_TOKEN) ] || { echo "$(DEV_TOKEN) missing: run make init-dev"; exit 1; }
	go run ./cmd/cauri ingestor --api-url http://localhost:$(DEV_API_PORT) \
		--token-file $(DEV_TOKEN) --sensor-id $(DEV_SENSOR) \
		--cowrie-log-dir $(COWRIE_DIR)/log/cowrie --cowrie-tty-dir $(COWRIE_DIR)/lib/cowrie/tty \
		--state-dir data/ingestor --log-level debug

dev-ui:
	cd ui && CAURI_API_URL=http://localhost:$(DEV_API_PORT) npm run dev

# Stops the dev Cowrie.
dev-down:
	$(DEV_COMPOSE) down

# Deletes cauri's dev data (database, recordings, ingestor state, token)
# but keeps Cowrie's. Stop dev-api and dev-ingestor first, then run
# make init-dev to get a new token.
dev-reset:
	rm -rf data/cauri.db data/cauri.db-* data/tty data/ingestor $(DEV_TOKEN)
