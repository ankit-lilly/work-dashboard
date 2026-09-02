# Radar Dashboard Makefile

BINARY_NAME=radar
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_DIR=bin
LDFLAGS=-s -w -X main.version=$(VERSION)

.PHONY: all build clean run run-mcp fmt tidy help css css-watch test

all: fmt tidy build

help:
	@echo "Usage:"
	@echo "  make build    - Build the optimized binary"
	@echo "  make run      - Run the project locally"
	@echo "  make run-mcp  - Run the MCP server (stdio)"
	@echo "  make css      - Build Tailwind + DaisyUI CSS"
	@echo "  make css-watch - Watch and rebuild Tailwind CSS"
	@echo "  make fmt      - Format Go code"
	@echo "  make tidy     - Tidy Go modules"
	@echo "  make clean    - Remove build artifacts"

build:
	@tailwindcss -i static/src/tailwind.css -o static/css/app.css --minify
	@echo "Building optimized binary ($(VERSION))..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -trimpath -o $(BUILD_DIR)/$(BINARY_NAME) -ldflags="$(LDFLAGS)" .
	@echo "Binary built at $(BUILD_DIR)/$(BINARY_NAME)"

run: fmt tidy
	@echo "Starting Radar Dashboard..."
	go run -ldflags="-X main.version=$(VERSION)" . server

run-mcp: fmt tidy
	@echo "Starting Radar MCP..."
	go run -ldflags="-X main.version=$(VERSION)" . mcp

fmt:
	@echo "Formatting code..."
	go fmt ./...

test:
	@echo "Running tests..."
	go test ./...

tidy:
	@echo "Tidying modules..."
	go mod tidy

css:
	@echo "Building Tailwind + DaisyUI CSS..."
	@tailwindcss -i static/src/tailwind.css -o static/css/app.css --minify

css-watch:
	@echo "Watching Tailwind + DaisyUI CSS..."
	@tailwindcss -i static/src/tailwind.css -o static/css/app.css --watch

clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
