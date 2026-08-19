.PHONY: tools generate proto-lint go-mod-tidy go-test web-install web-generate web-lint web-typecheck web-test web-build test build verify-generated

PNPM ?= pnpm
FINCONFIG_TOOL_ROOT ?= $(CURDIR)/.cache/tools
BUF := $(FINCONFIG_TOOL_ROOT)/buf-1.58.0/buf/bin/buf

tools:
	FINCONFIG_TOOL_ROOT=$(FINCONFIG_TOOL_ROOT) ./tools/bootstrap-tools.sh

generate:
	FINCONFIG_TOOL_ROOT=$(FINCONFIG_TOOL_ROOT) ./tools/generate.sh
	$(PNPM) generate:api

proto-lint: tools
	cd contracts && BUF_CACHE_DIR=$(CURDIR)/.cache/buf $(BUF) lint
	cd contracts && BUF_CACHE_DIR=$(CURDIR)/.cache/buf $(BUF) format --diff --exit-code
	BUF_CACHE_DIR=$(CURDIR)/.cache/buf $(BUF) lint
	BUF_CACHE_DIR=$(CURDIR)/.cache/buf $(BUF) format --diff --exit-code

go-mod-tidy:
	cd contracts && GOWORK=off GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/go-mod go mod tidy -diff
	cd tools && GOWORK=off GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/go-mod go mod tidy -diff

go-test:
	GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/go-mod go test ./...
	cd contracts && GOWORK=off GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/go-mod go test ./...
	cd tools && GOWORK=off GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/go-mod go test ./...

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

test: proto-lint go-mod-tidy go-test web-lint web-typecheck web-test

build:
	GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/go-mod go build ./...
	cd contracts && GOWORK=off GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/go-mod go build ./...
	$(PNPM) build

verify-generated: generate
	git diff --exit-code -- contracts/gen/go contracts/kitex_gen contracts/proto gen/go kitex_gen api/proto web/admin-console/src/api/schema.d.ts
	@untracked="$$(git ls-files --others --exclude-standard -- contracts/gen/go contracts/kitex_gen gen/go kitex_gen web/admin-console/src/api/schema.d.ts)"; \
		if [ -n "$$untracked" ]; then \
			echo "untracked generated files:"; \
			echo "$$untracked"; \
			exit 1; \
		fi
