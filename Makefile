# VoltPanel — Entwicklungs- und Build-Ziele
#
# `make build` erzeugt die beiden Binaries mit eingebettetem Frontend.

SHELL := /bin/bash
MODULE := github.com/marion909/voltpanel
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(DATE)

BIN_DIR := bin
DIST_DIR := dist

# sha256sum gibt es auf Linux, shasum auf macOS — dieselbe Ausgabe.
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo 'shasum -a 256')

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[1m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: web
web: ## Baut das Frontend nach internal/webui/dist
	@./scripts/build-web.sh

.PHONY: build
build: web ## Baut volt und volt-agent (mit eingebettetem Frontend)
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/volt ./cmd/volt
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/volt-agent ./cmd/volt-agent
	@echo "→ $(BIN_DIR)/volt und $(BIN_DIR)/volt-agent ($(VERSION))"

.PHONY: build-go
build-go: ## Baut nur die Binaries, ohne das Frontend neu zu erzeugen
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/volt ./cmd/volt
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/volt-agent ./cmd/volt-agent

.PHONY: linux
linux: web ## Baut für linux/amd64 und linux/arm64
	@mkdir -p $(BIN_DIR)
	@for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(BIN_DIR)/volt_linux_$$arch ./cmd/volt; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o $(BIN_DIR)/volt-agent_linux_$$arch ./cmd/volt-agent; \
		echo "→ linux/$$arch"; \
	done

.PHONY: dist
dist: linux ## Schnürt die Offline-Installationspakete nach dist/
	@rm -rf $(DIST_DIR)
	@for arch in amd64 arm64; do \
		name="voltpanel_$(VERSION)_linux_$$arch"; \
		stage="$(DIST_DIR)/$$name"; \
		mkdir -p "$$stage/systemd"; \
		install -m 0755 $(BIN_DIR)/volt_linux_$$arch       "$$stage/volt"; \
		install -m 0755 $(BIN_DIR)/volt-agent_linux_$$arch "$$stage/volt-agent"; \
		install -m 0755 packaging/install.sh               "$$stage/install.sh"; \
		install -m 0644 packaging/systemd/*                "$$stage/systemd/"; \
		install -m 0644 README.md LICENSE                  "$$stage/"; \
		tar -C $(DIST_DIR) -czf "$$stage.tar.gz" "$$name"; \
		( cd $(DIST_DIR) && $(SHA256) "$$name.tar.gz" > "$$name.tar.gz.sha256" ); \
		rm -rf "$$stage"; \
		echo "→ $$stage.tar.gz"; \
	done

.PHONY: test
test: ## Führt alle Tests aus
	go test ./...

.PHONY: test-race
test-race: ## Tests mit Race-Detector
	go test -race ./...

.PHONY: cover
cover: ## Testabdeckung als HTML
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "→ coverage.html"

.PHONY: lint
lint: ## gofmt, go vet und die Dateimodi der Skripte
	@test -z "$$(gofmt -l cmd internal)" || { echo "nicht formatiert:"; gofmt -l cmd internal; exit 1; }
	go vet ./...
	@$(MAKE) --no-print-directory lint-scripts

.PHONY: lint-scripts
lint-scripts: ## Prüft, ob alle Skripte in git ausführbar sind
	@missing="$$(git ls-files -s '*.sh' | awk '$$1 != "100755" {print $$4}')"; \
	if [ -n "$$missing" ]; then \
		echo "Diesen Skripten fehlt in git das x-Bit:"; \
		echo "$$missing" | sed 's/^/  /'; \
		echo "Beheben mit: git update-index --chmod=+x <datei>"; \
		exit 1; \
	fi
	@for f in $$(git ls-files '*.sh'); do bash -n "$$f" || exit 1; done

.PHONY: dev
dev: build-go ## Startet das Panel lokal gegen ./tmp (ohne Agent)
	@mkdir -p tmp/{etc,data,log,www,backups,run}
	@printf 'listen_addr: 127.0.0.1\nport: 8443\ndata_dir: $(PWD)/tmp/data\nconfig_dir: $(PWD)/tmp/etc\nlog_dir: $(PWD)/tmp/log\nsites_dir: $(PWD)/tmp/www\nbackup_dir: $(PWD)/tmp/backups\ndb_path: $(PWD)/tmp/data/volt.db\nsocket_path: $(PWD)/tmp/run/agent.sock\nsecret_key_path: $(PWD)/tmp/etc/secret.key\ncert_dir: $(PWD)/tmp/data/certs\n' > tmp/etc/config.yaml
	VOLT_CONFIG=$(PWD)/tmp/etc/config.yaml $(BIN_DIR)/volt serve --dev-origin http://localhost:5173

.PHONY: dev-web
dev-web: ## Startet den Vite-Dev-Server (Hot Reload gegen `make dev`)
	cd web && npm run dev

.PHONY: clean
clean: ## Entfernt Build-Artefakte
	rm -rf $(BIN_DIR) dist coverage.out coverage.html internal/webui/dist/assets
