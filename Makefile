# MemorY build targets. The product binary is pure Go and statically linked.

GO      ?= go
JOBS    ?= 4
PKG     := github.com/agenticode/my
BIN     := my
DIST    := dist

# What a build is stamped with: the version (the tag of a release, else what
# git describe says), the commit and the time of that commit.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell git log -1 --format=%cI 2>/dev/null)

LDFLAGS := -s -w \
	-X $(PKG)/internal/buildinfo.Version=$(VERSION) \
	-X $(PKG)/internal/buildinfo.Commit=$(COMMIT) \
	-X $(PKG)/internal/buildinfo.Date=$(DATE)
BUILD   := -trimpath -p $(JOBS) -ldflags '$(LDFLAGS)'

PLATFORMS := linux/amd64 linux/arm64 darwin/arm64
empty :=
space := $(empty) $(empty)
comma := ,
DIST_ARGS := -version $(VERSION) -commit $(COMMIT) -date $(DATE) -platforms $(subst $(space),$(comma),$(PLATFORMS))

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
	./internal/adapter/rasdaemon:FuzzRows \
	./internal/adapter/rasdaemon:FuzzStoreSettings \
	./internal/decode/ipmi:FuzzParseRecord \
	./internal/adapter/sel:FuzzCollect \
	./internal/adapter/redfish:FuzzCollect \
	./internal/adapter/nvidia:FuzzCollect \
	./internal/adapter/mcelog:FuzzCollect \
	./internal/adapter/fmfm:FuzzCollect \
	./internal/adapter/dcbrain:FuzzTables \
	./internal/injection:FuzzRead \
	./internal/engine:FuzzConfigRead \
	./internal/i18n:FuzzFormat \
	./internal/bundle:FuzzText \
	./internal/bundle:FuzzReadManifest

.PHONY: all build test test-race golden examples examples-case fuzz lint fmt fmt-check vet cross dist dist-check clean

all: lint test build

build:
	$(GO) build $(BUILD) -o bin/$(BIN) ./cmd/my

test:
	$(GO) test -p $(JOBS) ./...

# The race detector needs cgo; the product build stays CGO_ENABLED=0. The
# tests of the commands scan host trees and take several minutes with the
# detector on a small machine, more than the ten minutes a package gets by
# default.
test-race:
	CGO_ENABLED=1 $(GO) test -race -timeout 30m -p $(JOBS) ./...

# The figures are pinned by golden files. After an intended change of the
# output, rewrite them and review the diff.
golden:
	UPDATE_GOLDEN=1 $(GO) test -p $(JOBS) ./internal/view/ ./internal/term/ ./internal/report/ ./cmd/my/

# The files under examples/ are made by the tool itself, at a fixed time and
# by a build without stamps, so that they come out the same on every
# machine. A test compares them with what the command writes.
EXAMPLE_NOW := 2026-11-13T09:30:00Z
MY_EXAMPLE  := MY_NOW=$(EXAMPLE_NOW) $(GO) run -buildvcs=false ./cmd/my

# The report and the figures of the example case. Run it after a change of
# the report or of the engine, and review the diff.
examples:
	rm -f examples/figures/*.svg
	$(MY_EXAMPLE) report examples/demo.mycase -o examples/demo-report.html --figures examples/figures --force

# The example case: a bundle of the synthetic demo host with CPU0_C1 as its
# subject. A bundle holds a ledger, which names itself anew every time it
# is made; make the case again only when it has to change (a new ledger
# schema, another demo scenario), then run "make examples".
examples-case:
	@set -e; tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	$(GO) run -buildvcs=false ./cmd/my synth -scenario demo -ledger "$$tmp/demo.db" > /dev/null; \
	$(MY_EXAMPLE) open "$$tmp/demo.db" bundle CPU0_C1 -o examples/demo.mycase --force

fuzz:
	@set -e; for t in $(FUZZ_TARGETS); do \
		pkg=$${t%%:*}; fn=$${t##*:}; \
		echo "fuzz $$fn ($(FUZZTIME))"; \
		$(GO) test -run '^$$' -fuzz "^$$fn\$$" -fuzztime $(FUZZTIME) -parallel $(JOBS) $$pkg; \
	done

lint: fmt-check vet

fmt:
	gofmt -w cmd internal tools

fmt-check:
	@out="$$(gofmt -l cmd internal tools)"; \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

cross:
	@set -e; for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "build $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch $(GO) build $(BUILD) -o $(DIST)/$(BIN)-$$os-$$arch ./cmd/my; \
	done

# Release archives: $(DIST)/my_<version>_<os>_<arch>.tar.gz, one per
# platform, and $(DIST)/SHA256SUMS. The same commit built with the same Go
# toolchain gives the same bytes on any machine (see tools/dist).
dist:
	$(GO) run ./tools/dist $(DIST_ARGS) -out $(DIST)

# Builds the archives twice and compares the checksums: once from this
# tree, once from a copy of it in another directory with an empty build
# cache. A path of the build machine or a time of day in the output would
# make the two differ.
dist-check:
	@set -e; tmp="$$(mktemp -d)"; trap 'chmod -R u+w "$$tmp"; rm -rf "$$tmp"' EXIT; \
	mkdir "$$tmp/src"; \
	tar -c --exclude ./.git --exclude ./$(DIST) --exclude ./bin . | tar -x -C "$$tmp/src"; \
	$(GO) run ./tools/dist $(DIST_ARGS) -out "$$tmp/a" > /dev/null; \
	GOCACHE="$$tmp/cache" $(GO) run ./tools/dist $(DIST_ARGS) -root "$$tmp/src" -out "$$tmp/b" > /dev/null; \
	if cmp -s "$$tmp/a/SHA256SUMS" "$$tmp/b/SHA256SUMS"; then \
		echo "two builds, the same archives:"; cat "$$tmp/a/SHA256SUMS"; \
	else \
		echo "two builds of one commit differ:"; diff "$$tmp/a/SHA256SUMS" "$$tmp/b/SHA256SUMS"; exit 1; \
	fi

clean:
	rm -rf bin $(DIST)
