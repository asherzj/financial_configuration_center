.PHONY: tools generate proto-lint go-test web-install web-generate web-lint web-typecheck web-test web-build test build verify-generated

PNPM ?= pnpm
FINCONFIG_TOOL_ROOT ?= $(CURDIR)/.cache/tools
BUF := $(FINCONFIG_TOOL_ROOT)/buf-1.58.0/buf/bin/buf

tools:
	FINCONFIG_TOOL_ROOT=$(FINCONFIG_TOOL_ROOT) ./tools/bootstrap-tools.sh

generate:
	FINCONFIG_TOOL_ROOT=$(FINCONFIG_TOOL_ROOT) ./tools/generate.sh
	$(PNPM) generate:api

proto-lint: tools
	BUF_CACHE_DIR=$(CURDIR)/.cache/buf $(BUF) lint
	BUF_CACHE_DIR=$(CURDIR)/.cache/buf $(BUF) format --diff --exit-code

go-test:
	GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/go-mod go test ./...

web-install:
	$(PNPM) install --frozen-lockfile

web-generate:
	$(PNPM) generate:api

web-lint:
	$(PNPM) lint

web-typecheck:
	$(PNPM) typecheck

web-test:
	$(PNPM) test

web-build:
	$(PNPM) build

test: proto-lint go-test web-lint web-typecheck web-test

build:
	GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/go-mod go build ./...
	$(PNPM) build

verify-generated: generate
	git diff --exit-code -- gen/go kitex_gen web/admin-console/src/api/schema.d.ts
