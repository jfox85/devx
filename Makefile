# Variables
BINARY_NAME=devx
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X github.com/jfox85/devx/version.Version=$(VERSION) -X github.com/jfox85/devx/version.GitCommit=$(GIT_COMMIT) -X github.com/jfox85/devx/version.BuildDate=$(BUILD_DATE)"

# Build the Svelte SPA
.PHONY: web-build
web-build:
	cd web/app && npm install && npm run build

# Development server for the SPA
.PHONY: web-dev
web-dev:
	cd web/app && npm run dev

# Default target
.PHONY: build
build: web-build
	go build $(LDFLAGS) -o $(BINARY_NAME) .

# Clean build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY_NAME)
	rm -rf web/dist

# Install to GOPATH/bin
.PHONY: install
install:
	go install $(LDFLAGS) .

# Development build (no version info)
.PHONY: dev
dev:
	go build -o $(BINARY_NAME) .

# Run tests
.PHONY: test
test:
	go test ./...

# Show build variables
.PHONY: info
info:
	@echo "Build Info:"
	@echo "  Binary: $(BINARY_NAME)"
	@echo "  Version: $(VERSION)"
	@echo "  Commit: $(GIT_COMMIT)"
	@echo "  Date: $(BUILD_DATE)"

# Help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build      - Build Svelte app then Go binary with version info"
	@echo "  web-build  - Build the Svelte SPA only"
	@echo "  web-dev    - Start the Svelte dev server"
	@echo "  dev        - Quick development build (Go only)"
	@echo "  install    - Install to GOPATH/bin"
	@echo "  clean      - Remove build artifacts"
	@echo "  test       - Run tests"
	@echo "  info       - Show build variables"
	@echo "  help       - Show this help"