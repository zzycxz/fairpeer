# fairpeer — developer tasks (see CONTRIBUTING.md)
# Root module only; desktop/ is a separate Wails module (see `test-desktop`).

# Native builds need the .exe suffix on Windows (Go won't add it when -o is set);
# cross always spells it out explicitly.
ifeq ($(OS),Windows_NT)
BINARY  := bin/fairpeer.exe
else
BINARY  := bin/fairpeer
endif
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

## hooks: install git hooks (pre-push: go vet). On Windows run from Git Bash.
hooks:
	@mkdir -p .git/hooks
	@printf '#!/bin/sh\ngo vet ./... || exit 1\n' > .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "installed .git/hooks/pre-push (go vet)"

## cross: cross-compile the CLI to seven platforms
# The CLI is constructively CGO-Free (no import "C" under cmd/); the env var
# is pinned so a host C toolchain can never silently change that guarantee.
# GOAMD64=v1 is the domestic-x86 floor: Zhaoxin KX-5000/KX-6000 (the largest
# installed base) have AVX but NO AVX2 — a v3 build SIGILLs there at first
# exec. Do not raise without a Zhaoxin/Hygon regression pass.
# loong64 targets new-world LoongArch only (Loongnix 25, UOS V25, Kylin V11,
# kernel >= 5.19) — old-world installs (Loongnix 20 / UOS V20 loongarch64 /
# Kylin V10 loongarch) cannot run upstream Go binaries at all.
cross:
	GOAMD64=v1 GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o bin/fairpeer-linux-amd64       ./cmd/fairpeer
	GOAMD64=v1 GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o bin/fairpeer-linux-arm64       ./cmd/fairpeer
	GOAMD64=v1 GOOS=linux   GOARCH=loong64 CGO_ENABLED=0 $(GO) build -o bin/fairpeer-linux-loong64   ./cmd/fairpeer
	GOAMD64=v1 GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o bin/fairpeer-darwin-amd64      ./cmd/fairpeer
	GOAMD64=v1 GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o bin/fairpeer-darwin-arm64      ./cmd/fairpeer
	GOAMD64=v1 GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o bin/fairpeer-windows-amd64.exe ./cmd/fairpeer
	GOAMD64=v1 GOOS=windows GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o bin/fairpeer-windows-arm64.exe ./cmd/fairpeer

## frontend: build the desktop frontend (install deps first)
frontend:
	cd desktop/frontend && npm install && npm run build

clean:
	rm -rf bin/
