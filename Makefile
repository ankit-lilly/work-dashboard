# CAMP Job Viewer Makefile

BINARY_NAME=work-dashboard
VERSION=0.1.0
BUILD_DIR=bin

.PHONY: all build clean run fmt tidy help css css-watch

all: fmt tidy build

help:
	@echo "Usage:"
	@echo "  make build    - Build the optimized binary"
	@echo "  make run      - Run the project locally"
	@echo "  make css      - Build Tailwind + DaisyUI CSS"
	@echo "  make css-watch - Watch and rebuild Tailwind CSS"
	@echo "  make fmt      - Format Go code"
	@echo "  make tidy     - Tidy Go modules"
	@echo "  make clean    - Remove build artifacts"

build:
	@tailwindcss -i static/src/tailwind.css -o static/css/app.css --minify
	@echo "Building optimized binary..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -o $(BUILD_DIR)/$(BINARY_NAME) -ldflags="-s -w" main.go
	@echo "Binary built at $(BUILD_DIR)/$(BINARY_NAME)"

run: fmt tidy
	@echo "Starting CAMP Job Viewer..."
	go run main.go

fmt:
	@echo "Formatting code..."
	go fmt ./...

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
