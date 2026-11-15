# MemorY build targets. The product binary is pure Go and statically linked.

GO      ?= go
JOBS    ?= 4
PKG     := github.com/agenticode/my
BIN     := my
DIST    := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)

LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.Version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.Commit=$(COMMIT)
BUILD   := -trimpath -p $(JOBS) -ldflags '$(LDFLAGS)'

PLATFORMS := linux/amd64 linux/arm64 darwin/arm64

export CGO_ENABLED := 0

# Fuzz targets as "package:function". FUZZTIME is the run time of each.
FUZZTIME ?= 10s
FUZZ_TARGETS := \
	./internal/decode/edac:FuzzParseDetail \
	./internal/decode/cper:FuzzParseText \
	./internal/decode/cper:FuzzParseBinary \
	./internal/adapter/smbios:FuzzTables \
	./internal/adapter/smbios:FuzzDmidecode \
	./internal/adapter/klog:FuzzCollect \
	./internal/decode/ipmi:FuzzParseRecord \
	./internal/adapter/sel:FuzzCollect \
	./internal/adapter/redfish:FuzzCollect \
	./internal/adapter/nvidia:FuzzCollect \
	./internal/adapter/dcbrain:FuzzTables \
	./internal/engine:FuzzConfigRead \
	./internal/i18n:FuzzFormat \
	./internal/bundle:FuzzText \
	./internal/bundle:FuzzReadManifest

.PHONY: all build test test-race golden fuzz lint fmt fmt-check vet cross clean

all: lint test build

build:
	$(GO) build $(BUILD) -o bin/$(BIN) ./cmd/my

test:
	$(GO) test -p $(JOBS) ./...

# The race detector needs cgo; the product build stays CGO_ENABLED=0.
test-race:
	CGO_ENABLED=1 $(GO) test -race -p $(JOBS) ./...

# The figures are pinned by golden files. After an intended change of the
# output, rewrite them and review the diff.
golden:
	UPDATE_GOLDEN=1 $(GO) test -p $(JOBS) ./internal/view/ ./internal/term/ ./cmd/my/

fuzz:
	@set -e; for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; fn=$${t##*:}; \
		echo "fuzz $$fn ($(FUZZTIME))"; \
		$(GO) test -run '^$$' -fuzz "^$$fn\$$" -fuzztime $(FUZZTIME) -parallel $(JOBS) $$pkg; \
	done

lint: fmt-check vet

fmt:
	gofmt -w cmd internal

fmt-check:
	@out="$$(gofmt -l cmd internal)"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

cross:
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "build $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch $(GO) build $(BUILD) -o $(DIST)/$(BIN)-$$os-$$arch ./cmd/my; \
	done

clean:
	rm -rf bin $(DIST)
