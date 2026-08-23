APP := tadmin
VERSION ?= v0.1.0-dev
GO ?= go
DIST := dist
LDFLAGS := -s -w -X tadmin/internal/app.Version=$(VERSION)

ifeq ($(OS),Windows_NT)
SHELL := sh.exe
endif

.PHONY: fmt test vet build release

fmt:
	gofmt -w $$(find cmd internal assets -type f -name '*.go')

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

build:
	@:; mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/tadmin

release:
	@:; mkdir -p $(DIST)
	@:; rm -f $(DIST)/$(APP)-linux-amd64 $(DIST)/$(APP)-linux-arm64 $(DIST)/SHA256SUMS
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP)-linux-amd64 ./cmd/tadmin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP)-linux-arm64 ./cmd/tadmin
	cd $(DIST) && sha256sum $(APP)-linux-amd64 $(APP)-linux-arm64 > SHA256SUMS
