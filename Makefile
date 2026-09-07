# fairpeer — developer tasks (see CONTRIBUTING.md)
# Root module only; desktop/ is a separate Wails module (see `test-desktop`).

BINARY  := bin/fairpeer
GO      := go
VERSION ?= dev

.PHONY: build test test-desktop vet fmt hooks cross frontend clean

## build: compile the CLI binary into bin/
build:
	$(GO) build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/fairpeer

## test: run the root-module test suite
test:
	$(GO) test ./...

## test-desktop: run the desktop (Wails) module test suite
test-desktop:
	cd desktop && $(GO) test ./...

## vet: static analysis (root module)
vet:
	$(GO) vet ./...

## fmt: format all Go sources
fmt:
	gofmt -w .

## hooks: install git hooks (pre-push: go vet)
hooks:
	@mkdir -p .git/hooks
	@printf '#!/bin/sh\ngo vet ./... || exit 1\n' > .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "installed .git/hooks/pre-push (go vet)"

## cross: cross-compile the CLI to six platforms
cross:
	GOOS=linux   GOARCH=amd64 $(GO) build -o bin/fairpeer-linux-amd64       ./cmd/fairpeer
	GOOS=linux   GOARCH=arm64 $(GO) build -o bin/fairpeer-linux-arm64       ./cmd/fairpeer
	GOOS=darwin  GOARCH=amd64 $(GO) build -o bin/fairpeer-darwin-amd64      ./cmd/fairpeer
	GOOS=darwin  GOARCH=arm64 $(GO) build -o bin/fairpeer-darwin-arm64      ./cmd/fairpeer
	GOOS=windows GOARCH=amd64 $(GO) build -o bin/fairpeer-windows-amd64.exe ./cmd/fairpeer
	GOOS=windows GOARCH=arm64 $(GO) build -o bin/fairpeer-windows-arm64.exe ./cmd/fairpeer

## frontend: build the desktop frontend (install deps first)
frontend:
	cd desktop/frontend && npm install && npm run build

clean:
	rm -rf bin/
