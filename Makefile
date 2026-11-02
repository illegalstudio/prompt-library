.PHONY: build install test vet fmt clean release

BINARY := plib
PKG := ./cmd/plib
VERSION ?= $(patsubst v%,%,$(shell git describe --tags --dirty 2>/dev/null || echo 0.1.0-dev))
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS := -s -w -X github.com/illegalstudio/prompt-library/internal/version.Version=$(VERSION) -X github.com/illegalstudio/prompt-library/internal/version.Commit=$(COMMIT)

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

install:
	go install -trimpath -ldflags "$(LDFLAGS)" $(PKG)

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

clean:
	rm -f plib plib.exe

release:
	@scripts/release.sh
