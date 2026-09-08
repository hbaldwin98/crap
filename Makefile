GO ?= go
BUILD_DIR ?= bin
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

CRAP := $(BUILD_DIR)/crap
CRAP_MUTATE := $(BUILD_DIR)/crap-mutate

GO_SOURCES := $(shell find . -name '*.go' -not -path './$(BUILD_DIR)/*') go.mod go.sum

.PHONY: all build install gh-install test vet clean

all: build

build: $(CRAP) $(CRAP_MUTATE)

$(BUILD_DIR):
	mkdir -p "$@"

$(CRAP): $(GO_SOURCES) | $(BUILD_DIR)
	$(GO) build -trimpath -o "$@" ./cmd/crap

$(CRAP_MUTATE): $(GO_SOURCES) | $(BUILD_DIR)
	$(GO) build -trimpath -o "$@" ./cmd/crap-mutate

install: build
	mkdir -p "$(BINDIR)"
	cp "$(CRAP)" "$(BINDIR)/crap"
	cp "$(CRAP_MUTATE)" "$(BINDIR)/crap-mutate"

gh-install: install
	@test -n "$(shell command -v gh 2>/dev/null)" || (printf '%s\n' 'gh-install requires GitHub CLI (gh) on PATH' >&2; exit 1)
	gh alias set --shell --clobber crap 'crap "$$@"'
	gh alias set --shell --clobber crap-mutate 'crap-mutate "$$@"'

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf "$(BUILD_DIR)"
