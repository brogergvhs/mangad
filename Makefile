APP             := kaodoku
BIN             := bin
DIST            := dist
MAIN            := ./main.go

GOCMD           := go
GOBUILD         := $(GOCMD) build
GORUN           := $(GOCMD) run
GOCLEAN         := $(GOCMD) clean
GOFMT           := $(GOCMD) fmt
GOVET           := $(GOCMD) vet
GOTEST          := $(GOCMD) test
STATICCHECK     := staticcheck
GOCACHE         ?= $(CURDIR)/.cache/go-build

VERSION         := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LD_FLAGS        := -X 'github.com/brogergvhs/kaodoku/cmd.Version=$(VERSION)'

.PHONY: all build install run fmt vet test test-race lint check clean package

all: build

build:
	@echo "Building $(APP) (version: $(VERSION))..."
	@mkdir -p $(BIN)
	$(GOBUILD) -ldflags "$(LD_FLAGS)" -o $(BIN)/$(APP) $(MAIN)
	@echo "Binary: $(BIN)/$(APP)"

install: build
	@echo "Installing $(APP) into /usr/local/bin..."
	@sudo install -m 0755 $(BIN)/$(APP) /usr/local/bin/$(APP)
	@echo "Installed as /usr/local/bin/$(APP)"

run:
	$(GORUN) $(MAIN) $(ARGS)

fmt:
	$(GOFMT) ./...

vet:
	$(GOVET) ./...

test:
	@mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) $(GOTEST) ./...

test-race:
	@mkdir -p $(GOCACHE)
	GOCACHE=$(GOCACHE) $(GOTEST) -race ./...

lint:
	$(STATICCHECK) ./...

check: fmt vet test lint

clean:
	@echo "Cleaning..."
	@rm -rf $(BIN) $(DIST)
	@$(GOCLEAN)
	@echo "Cleaned"

package:
	@echo "Packaging cross-platform binaries (version: $(VERSION))..."
	@mkdir -p $(DIST)

	# Linux
	GOOS=linux   GOARCH=amd64  $(GOBUILD) -ldflags "$(LD_FLAGS)" -o $(DIST)/$(APP)-linux-amd64   $(MAIN)
	GOOS=linux   GOARCH=arm64  $(GOBUILD) -ldflags "$(LD_FLAGS)" -o $(DIST)/$(APP)-linux-arm64   $(MAIN)

	# macOS
	GOOS=darwin  GOARCH=amd64  $(GOBUILD) -ldflags "$(LD_FLAGS)" -o $(DIST)/$(APP)-darwin-amd64  $(MAIN)
	GOOS=darwin  GOARCH=arm64  $(GOBUILD) -ldflags "$(LD_FLAGS)" -o $(DIST)/$(APP)-darwin-arm64  $(MAIN)

	# Windows
	GOOS=windows GOARCH=amd64  $(GOBUILD) -ldflags "$(LD_FLAGS)" -o $(DIST)/$(APP)-windows-amd64.exe $(MAIN)

	@echo "Build complete."
	@echo "Files in $(DIST):"
	@ls -1 $(DIST)
