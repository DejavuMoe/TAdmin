APP     := tadmin
VERSION ?= v2.0.0-dev
GO      ?= go
DIST    := dist
LDFLAGS := -s -w -X tadmin/internal/app.Version=$(VERSION)

ifeq ($(OS),Windows_NT)
SHELL := sh.exe
endif

.PHONY: fmt vet test race build release docker

fmt:
	gofmt -w cmd internal assets

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

race:
	CGO_ENABLED=1 $(GO) test -race ./...

build:
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/tadmin

release:
	@mkdir -p $(DIST)
	@rm -f $(DIST)/$(APP)-linux-* $(DIST)/SHA256SUMS
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP)-linux-amd64 ./cmd/tadmin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(APP)-linux-arm64 ./cmd/tadmin
	cd $(DIST) && sha256sum $(APP)-linux-amd64 $(APP)-linux-arm64 > SHA256SUMS

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(APP):$(VERSION) .
