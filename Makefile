# Variables
BINARY_NAME=devx
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X github.com/jfox85/devx/version.Version=$(VERSION) -X github.com/jfox85/devx/version.GitCommit=$(GIT_COMMIT) -X github.com/jfox85/devx/version.BuildDate=$(BUILD_DATE)"

# Default target
.PHONY: build
build:
	go build $(LDFLAGS) -o $(BINARY_NAME) .

# Clean build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY_NAME)

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
	@echo "  build    - Build binary with version info"
	@echo "  dev      - Quick development build"
	@echo "  install  - Install to GOPATH/bin"
	@echo "  clean    - Remove build artifacts"
	@echo "  test     - Run tests"
	@echo "  info     - Show build variables"
	@echo "  help     - Show this help"