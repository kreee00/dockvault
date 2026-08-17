BINARY      := dockvault
CMD_PATH    := ./cmd/dockvault
BUILD_DIR   := bin
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X dockvault/internal/version.Version=$(VERSION) \
           -X dockvault/internal/version.Commit=$(COMMIT) \
           -X dockvault/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build
build: ## Compile to bin/dockvault (static binary)
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) $(CMD_PATH)

.PHONY: test
test: ## Run all unit tests
	go test ./...

.PHONY: test-coverage
test-coverage: ## Generate coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

.PHONY: install
install: build ## Install to /usr/local/bin/dockvault
	install -m 0755 $(BUILD_DIR)/$(BINARY) /usr/local/bin/$(BINARY)

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR) coverage.out coverage.html

.PHONY: docker-build
docker-build: ## Build Linux binary inside a Docker container (cross-compilation)
	docker run --rm -v "$$PWD":/src -w /src golang:1.21 \
		env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 $(CMD_PATH)

.PHONY: build-all
build-all: ## Cross-compile for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64    $(CMD_PATH)
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64    $(CMD_PATH)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-amd64   $(CMD_PATH)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-arm64   $(CMD_PATH)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe $(CMD_PATH)

.PHONY: fmt
fmt: ## gofmt everything
	gofmt -l -w .

.PHONY: vet
vet: ## go vet everything
	go vet ./...

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-16s\033[0m %s\n", $$1, $$2}'
