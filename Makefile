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

# Fuzz targets as "package:function"; docs/fuzzing.md says what each one
# reads. FUZZTIME is the run of each: a duration, or a number of calls
# ("20000x"). FUZZMINIMIZE bounds the work spent on making an input
# smaller that reached new code: the default of go test, 60 seconds per
# input, leaves little of a run for fuzzing.
FUZZTIME ?= 10s
FUZZMINIMIZE ?= 200x
FUZZ_TARGETS := \
	./internal/decode/edac:FuzzParseDetail \
	./internal/decode/cper:FuzzParseText \
	./internal/decode/cper:FuzzParseBinary \
	./internal/decode/cper:FuzzParseStatusBlocks \
	./internal/decode/mce:FuzzDecode \
	./internal/decode/ipmi:FuzzParseRecord \
	./internal/decode/ipmi:FuzzLabels \
	./internal/decode/virt:FuzzNames \
	./internal/adapter/smbios:FuzzTables \
	./internal/adapter/smbios:FuzzEntryPoint \
	./internal/adapter/smbios:FuzzDmidecode \
	./internal/adapter/klog:FuzzCollect \
	./internal/adapter/crashlog:FuzzParts \
	./internal/adapter/bert:FuzzCollect \
	./internal/adapter/rasdaemon:FuzzRows \
	./internal/adapter/rasdaemon:FuzzDatabase \
	./internal/adapter/rasdaemon:FuzzStoreSettings \
	./internal/adapter/edacsysfs:FuzzCollect \
	./internal/adapter/hostinfo:FuzzCollect \
	./internal/adapter/sel:FuzzCollect \
	./internal/adapter/redfish:FuzzCollect \
	./internal/adapter/nvidia:FuzzCollect \
	./internal/adapter/mcelog:FuzzCollect \
	./internal/adapter/fmfm:FuzzCollect \
	./internal/adapter/dcbrain:FuzzTables \
	./internal/adapter/dcbrain:FuzzArchive \
	./internal/scan:FuzzDetect \
	./internal/injection:FuzzRead \
	./internal/engine:FuzzConfigRead \
	./internal/i18n:FuzzFormat \
	./internal/bundle:FuzzText \
	./internal/bundle:FuzzReadManifest \
	./internal/bundle:FuzzMembers \
	./internal/plugin:FuzzResponse \
	./internal/term:FuzzSafe \
	./internal/redfish/client:FuzzParseTarget \
	./internal/redfish/client:FuzzParseFingerprint \
	./internal/redfish/client:FuzzResolve \
	./internal/redfish/client:FuzzScrub \
	./internal/redfish/client:FuzzAnswer \
	./internal/redfish/collect:FuzzFilePath \
	./internal/redfish/collect:FuzzParseManifest \
	./internal/redfish/collect:FuzzCrawl \
	./internal/memras:FuzzParse \
	./internal/fwmap:FuzzParse \
	./internal/fwmap:FuzzClassify \
	./internal/adapter/redfish:FuzzRegistry \
	./internal/preflight:FuzzHost \
	./internal/eventstream:FuzzLine \
	./internal/adapter/vendorlog:FuzzCollect \
	./internal/explorer:FuzzDimms \
	./internal/explorer:FuzzCaches \
	./internal/explorer:FuzzNuma \
	./internal/explorer:FuzzSystem \
	./internal/explorer:FuzzPage \
	./internal/explorer:FuzzProcesses \
	./internal/explorer:FuzzJVM \
	./internal/explorer:FuzzFileCache \
	./internal/explorer:FuzzProcessNuma \
	./internal/explorer:FuzzMglru \
	./internal/explorer:FuzzLoad \
	./internal/explorer:FuzzFormat

.PHONY: all build test test-race golden examples examples-case fuzz lint fmt fmt-check vet cross dist dist-check clean

all: lint test build

build:
	$(GO) build $(BUILD) -o bin/$(BIN) ./cmd/my

test:
	$(GO) test -p $(JOBS) ./...

# The race detector needs cgo; the product build stays CGO_ENABLED=0. The
# tests of the commands scan host trees into new ledgers, and SQLite runs
# slowly under the detector: on the CI runner the package cmd/my took 1,295
# to 1,735 seconds with go1.26.0 and went over 30 minutes with go1.26.11.
# A package gets an hour.
test-race:
	CGO_ENABLED=1 $(GO) test -race -timeout 60m -p $(JOBS) ./...

# The figures are pinned by golden files. After an intended change of the
# output, rewrite them and review the diff.
golden:
	UPDATE_GOLDEN=1 $(GO) test -p $(JOBS) ./internal/view/ ./internal/term/ ./internal/report/ ./cmd/my/

# The files under examples/ are made by the tool itself, at a fixed time and
# by a build without stamps, so that they come out the same on every
# machine. A test compares them with what the command writes.
EXAMPLE_NOW := 2026-11-13T09:30:00Z
MY_EXAMPLE  := MY_NOW=$(EXAMPLE_NOW) $(GO) run -buildvcs=false ./cmd/my

# The report and the figures of the example case, in English and, under
# examples/ko, in Korean. Run it after a change of the report or of the
# engine, and review the diff.
examples:
	rm -f examples/figures/*.svg examples/ko/figures/*.svg
	$(MY_EXAMPLE) report examples/demo.mycase -o examples/demo-report.html --figures examples/figures --force
	$(MY_EXAMPLE) report examples/demo.mycase -o examples/ko/demo-report.html --figures examples/ko/figures --lang ko --force

# The example cases: bundles of the synthetic demo host and of a host in a
# crash loop, with CPU0_C1 as their subject. A bundle holds a ledger, which
# names itself anew every time it is made; make the cases again only when
# they have to change (a new ledger schema, another scenario), then run
# "make examples".
examples-case:
	@set -e; tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	$(GO) run -buildvcs=false ./cmd/my synth -scenario demo -ledger "$$tmp/demo.db" > /dev/null; \
	$(MY_EXAMPLE) open "$$tmp/demo.db" bundle CPU0_C1 -o examples/demo.mycase --force; \
	$(GO) run -buildvcs=false ./cmd/my synth -scenario crash-loop -ledger "$$tmp/loop.db" > /dev/null; \
	$(MY_EXAMPLE) open "$$tmp/loop.db" bundle CPU0_C1 -o examples/crash-loop.mycase --force

fuzz:
	@GO="$(GO)" tools/fuzz.sh $(FUZZTIME) $(FUZZMINIMIZE) $(JOBS) $(FUZZ_TARGETS)

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
