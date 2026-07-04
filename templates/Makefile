.PHONY: all setup deps install-tools dev run-local dev-local build build-server build-frontend build-runtime test-go-race test-rust-loom communication-contracts lifecycle-contracts lifecycle-manifest frontend-prototype-runtime frontend-commands runtime-bindings foundation-transport-proto build-wasm build-wasm-dev build-rust-wasm wasm-manifest native-dev native-build native-mobile-init native-bench native-doctor delivery-metrics docgen test test-local-full test-local-services test-wasm test-e2e test-unit test-coverage test-integration test-load test-bench test-bench-frontend test-all test-frontend test-env-up test-env-down test-env-logs migrate-test migrate-test-down migrate-test-reset lint lint-go lint-rust lint-frontend lint-foundation verify clean help proto proto-ts docker-build docker-up docker-down docker-logs migrate-up migrate-down migrate-create foundation-update foundation-status check-rust check-go-fix check-go-static-analysis check-rust-static-analysis check-rust-runtime-practices check-doc-references check-markdown-frontmatter check-agent-contract check-practice-controls check-runtime-performance-contracts check-formal-methods check-operational-excellence check-coding-practices check-testing-practices check-go-concurrency-practices check-logging-practices check-metadata-practices check-dynamic-payload-practices check-database-practices check-atomic-lane-purity check-redis-practices check-river-practices check-migration-structure check-lifecycle-manifest check-app-security-profile check-domain-contract-consistency check-contract-drift check-server-kit-module-contract check-server-kit-usage check-project-scaffold

PROJECT_NAME := $(shell basename "$(CURDIR)" | sed 's/_v[0-9]*$$//')
GO_MODULE := $(shell grep -m1 "^module " go.mod 2>/dev/null | cut -d' ' -f2)
FOUNDATION_PATH := $(shell grep -m1 "^FOUNDATION_PATH=" .foundation 2>/dev/null | cut -d'=' -f2 | sed 's/\\ / /g')
PROTO_PATH := api/protos
WASM_PUBLIC_DIR ?= frontend/public
WASM_MODULE_DIR ?= $(WASM_PUBLIC_DIR)/modules
WASM_MANIFEST ?= $(WASM_PUBLIC_DIR)/runtime/wasm-manifest.json
WASM_OPT ?= wasm-opt
WASM_OPT_FLAGS ?= -Oz --enable-bulk-memory-opt --enable-nontrapping-float-to-int
BROTLI ?= brotli
RUST_TARGET ?= wasm32-unknown-unknown
RUST_WASM_PROFILE ?= release
RUST_WASM_DIR ?= rust
NATIVE_DIR ?= native

ifneq (,$(wildcard .env))
include .env
export
endif

GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GO_TEST_TIMEOUT ?= 120s
GO_CACHE_DIR ?= $(CURDIR)/.cache/go-build
GO_MOD_CACHE_DIR ?= $(CURDIR)/.cache/go-mod
GO_PACKAGE_EXCLUDE_RE ?= /node_modules/|/api/protos/
GO_COVERAGE_THRESHOLD ?= 95.0
FOUNDATION_VITEST_WORKERS ?= 0
FOUNDATION_VITEST_SERIAL ?= 1
MIGRATE_TEST_MAX_RETRIES ?= 12
MIGRATE_TEST_RETRY_SLEEP ?= 2
LOAD_RACE ?= 1
ifeq ($(LOAD_RACE),0)
LOAD_RACE_FLAG :=
else
LOAD_RACE_FLAG := -race
endif

DOCKER_BIN ?= $(shell if command -v docker >/dev/null 2>&1; then command -v docker; elif [ -x /Applications/Docker.app/Contents/Resources/bin/docker ]; then echo /Applications/Docker.app/Contents/Resources/bin/docker; else echo docker; fi)
DOCKER_COMPOSE ?= $(DOCKER_BIN) compose -f docker-compose.yml -f docker-compose.dev.yml
TEST_COMPOSE_PROJECT_NAME ?= $(PROJECT_NAME)_test
DOCKER_COMPOSE_TEST ?= $(DOCKER_BIN) compose -p $(TEST_COMPOSE_PROJECT_NAME) -f docker-compose.test.yml
DOCKER_BUILDKIT ?= 1
DOCKER_CACHE_NAMESPACE ?= ovasabi-foundation-v1
DOCKER_BUILD_ARGS ?= --build-arg CACHE_NAMESPACE=$(DOCKER_CACHE_NAMESPACE) --build-arg BUILDKIT_INLINE_CACHE=1
FRONTEND_SCRIPT_RUNNER ?= scripts/checks/frontend_script_runner.sh
TEST_ENV_FILE ?= .cache/test-env/runtime.env

DB_USER ?= postgres
DB_PASSWORD ?= postgres
DB_NAME ?= $(PROJECT_NAME)_dev
DB_PORT ?= 5432
POSTGRES_VERSION ?= 18
POSTGRES_PORT ?= $(DB_PORT)

TEST_DB_HOST ?= localhost
TEST_DB_PORT ?= 0
TEST_DB_USER ?= $(DB_USER)
TEST_DB_PASSWORD ?= $(DB_PASSWORD)
TEST_DB_NAME ?= $(PROJECT_NAME)_test
TEST_DATABASE_URL ?= postgresql://$(TEST_DB_USER):$(TEST_DB_PASSWORD)@$(TEST_DB_HOST):$(TEST_DB_PORT)/$(TEST_DB_NAME)?sslmode=disable

TEST_REDIS_HOST ?= localhost
TEST_REDIS_PORT ?= 0
TEST_REDIS_DB ?= 0
TEST_REDIS_PASSWORD ?=
ifeq ($(strip $(TEST_REDIS_PASSWORD)),)
TEST_REDIS_URL ?= redis://$(TEST_REDIS_HOST):$(TEST_REDIS_PORT)/$(TEST_REDIS_DB)
else
TEST_REDIS_URL ?= redis://:$(TEST_REDIS_PASSWORD)@$(TEST_REDIS_HOST):$(TEST_REDIS_PORT)/$(TEST_REDIS_DB)
endif
TEST_REQUIRES_POSTGIS ?= $(shell if [ -d migrations ] && grep -RiqE 'CREATE[[:space:]]+EXTENSION[[:space:]]+(IF[[:space:]]+NOT[[:space:]]+EXISTS[[:space:]]+)?"?postgis"?' migrations 2>/dev/null; then echo true; else echo false; fi)
TEST_POSTGIS_VERSION ?= 3.6
HOST_DOCKER_ARCH ?= $(shell uname -m | sed -e 's/^aarch64$$/arm64/' -e 's/^x86_64$$/amd64/')
HOST_DOCKER_PLATFORM ?= linux/$(HOST_DOCKER_ARCH)
ifeq ($(strip $(TEST_POSTGRES_IMAGE)),)
ifeq ($(TEST_REQUIRES_POSTGIS),true)
TEST_POSTGRES_IMAGE := postgis/postgis:$(POSTGRES_VERSION)-$(TEST_POSTGIS_VERSION)
TEST_POSTGRES_PLATFORM ?= linux/amd64
else
TEST_POSTGRES_IMAGE := postgres:$(POSTGRES_VERSION)-alpine
TEST_POSTGRES_PLATFORM ?= $(HOST_DOCKER_PLATFORM)
endif
endif
TEST_POSTGRES_PLATFORM ?= $(HOST_DOCKER_PLATFORM)

.DEFAULT_GOAL := help

all: build

setup: install-tools deps
	@echo "Setup complete"

install-tools:
	@echo "Installing tools..."
	@if [ -f go.mod ]; then \
		go install google.golang.org/protobuf/cmd/protoc-gen-go@latest; \
		go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi

deps:
	@echo "Installing dependencies..."
	@if [ -f go.mod ]; then go mod download && go mod tidy; fi
	@if [ -f frontend/package.json ]; then cd frontend && npm install; fi

proto:
	@echo "Generating Go protobuf code..."
	@[ -d $(PROTO_PATH) ] || exit 0
	@set -e; for proto_dir in $$(find $(PROTO_PATH) -mindepth 1 -maxdepth 1 -type d); do \
		latest_version_dir=$$(ls -d $$proto_dir/v*/ 2>/dev/null | sort -V | tail -n 1); \
		if [ -d "$$latest_version_dir" ]; then \
			echo "Processing protos in $$latest_version_dir..."; \
			for proto_file in $$(find $$latest_version_dir -name '*.proto'); do \
				protoc \
					-I=$(PROTO_PATH) \
					--go_out=$(PROTO_PATH) \
					--go_opt=paths=source_relative \
					--go-grpc_out=$(PROTO_PATH) \
					--go-grpc_opt=paths=source_relative \
					$$proto_file; \
			done; \
		fi; \
	done

proto-ts:
	@echo "Generating TypeScript protobuf code..."
	@[ -d frontend/src ] || exit 0
	@[ -d $(PROTO_PATH) ] || exit 0
	@if [ ! -f frontend/node_modules/.bin/protoc-gen-ts_proto ]; then \
		echo "Missing frontend/node_modules/.bin/protoc-gen-ts_proto. Run 'cd frontend && npm install' so ts-proto is installed."; \
		exit 1; \
	fi
	@mkdir -p frontend/src/types/protos
	@for proto_dir in $$(find $(PROTO_PATH) -mindepth 1 -maxdepth 1 -type d); do \
		latest_version_dir=$$(ls -d $$proto_dir/v*/ 2>/dev/null | sort -V | tail -n 1); \
		if [ -d "$$latest_version_dir" ]; then \
			protoc \
				-I=$(PROTO_PATH) \
				--plugin=protoc-gen-ts_proto=./frontend/node_modules/.bin/protoc-gen-ts_proto \
				--ts_proto_out=frontend/src/types/protos \
				--ts_proto_opt=esModuleInterop=true,forceLong=string,useOptionals=messages,outputEncodeMethods=false,outputJsonMethods=false,outputClientImpl=false \
				$$(find $$latest_version_dir -name '*.proto'); \
		fi; \
	done

frontend-prototype-runtime:
	@echo "Generating frontend prototype runtime..."
	@[ -d frontend/src ] || exit 0
	@[ -d $(PROTO_PATH) ] || exit 0
	@if [ -f scripts/checks/generate_frontend_prototype_runtime.mjs ]; then \
		node scripts/checks/generate_frontend_prototype_runtime.mjs --proto-root "$(PROTO_PATH)" --out "frontend/src/generated/prototypeRuntime.ts"; \
	elif [ -f foundation/tooling/scripts/generate_frontend_prototype_runtime.mjs ]; then \
		node foundation/tooling/scripts/generate_frontend_prototype_runtime.mjs --proto-root "$(PROTO_PATH)" --out "frontend/src/generated/prototypeRuntime.ts"; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -f "$(FOUNDATION_PATH)/tooling/scripts/generate_frontend_prototype_runtime.mjs" ]; then \
		node "$(FOUNDATION_PATH)/tooling/scripts/generate_frontend_prototype_runtime.mjs" --proto-root "$(PROTO_PATH)" --out "frontend/src/generated/prototypeRuntime.ts"; \
	else \
		echo "Frontend prototype runtime generator not found; skipping"; \
	fi

frontend-commands:
	@echo "Generating frontend route registry..."
	@[ -d frontend/src ] || exit 0
	@if [ ! -f go.mod ] || [ ! -d cmd/docgen ]; then \
		echo "No docgen command; skipping route registry generation"; exit 0; \
	fi
	@if ! grep -q 'route-catalog' cmd/docgen/main.go 2>/dev/null; then \
		echo "docgen lacks route-catalog subcommand; run foundation-update to adopt it; skipping"; exit 0; \
	fi
	@mkdir -p docs/references/lifecycle "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"
	@GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" $(GOCMD) run ./cmd/docgen route-catalog > docs/references/lifecycle/route_catalog.json
	@if [ -f scripts/checks/generate_frontend_commands.mjs ]; then \
		node scripts/checks/generate_frontend_commands.mjs --catalog "docs/references/lifecycle/route_catalog.json" --out "frontend/src/generated/runtimeRoutes.ts"; \
	elif [ -f foundation/tooling/scripts/generate_frontend_commands.mjs ]; then \
		node foundation/tooling/scripts/generate_frontend_commands.mjs --catalog "docs/references/lifecycle/route_catalog.json" --out "frontend/src/generated/runtimeRoutes.ts"; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -f "$(FOUNDATION_PATH)/tooling/scripts/generate_frontend_commands.mjs" ]; then \
		node "$(FOUNDATION_PATH)/tooling/scripts/generate_frontend_commands.mjs" --catalog "docs/references/lifecycle/route_catalog.json" --out "frontend/src/generated/runtimeRoutes.ts"; \
	else \
		echo "Frontend commands generator not found; skipping"; \
	fi

foundation-transport-proto:
	@if [ -x foundation/runtime-transport/scripts/generate_bindings.sh ]; then \
		foundation/runtime-transport/scripts/generate_bindings.sh; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -x "$(FOUNDATION_PATH)/runtime-transport/scripts/generate_bindings.sh" ]; then \
		"$(FOUNDATION_PATH)/runtime-transport/scripts/generate_bindings.sh"; \
	else \
		echo "Runtime transport binding generator not found; skipping"; \
	fi

communication-contracts: proto proto-ts frontend-prototype-runtime lifecycle-contracts lifecycle-manifest frontend-commands foundation-transport-proto runtime-bindings
	@echo "Communication contracts generated"

lifecycle-contracts:
	@echo "Generating lifecycle contract tests..."
	@[ -d $(PROTO_PATH) ] || exit 0
	@if [ -f scripts/checks/generate_lifecycle_contract_tests.mjs ]; then \
		node scripts/checks/generate_lifecycle_contract_tests.mjs --proto-root "$(PROTO_PATH)" --out "tests/contract/generated_lifecycle_test.go"; \
	elif [ -f foundation/tooling/scripts/generate_lifecycle_contract_tests.mjs ]; then \
		node foundation/tooling/scripts/generate_lifecycle_contract_tests.mjs --proto-root "$(PROTO_PATH)" --out "tests/contract/generated_lifecycle_test.go"; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -f "$(FOUNDATION_PATH)/tooling/scripts/generate_lifecycle_contract_tests.mjs" ]; then \
		node "$(FOUNDATION_PATH)/tooling/scripts/generate_lifecycle_contract_tests.mjs" --proto-root "$(PROTO_PATH)" --out "tests/contract/generated_lifecycle_test.go"; \
	else \
		echo "Lifecycle contract generator not found; skipping"; \
	fi

lifecycle-manifest:
	@echo "Generating lifecycle manifest..."
	@[ -d $(PROTO_PATH) ] || exit 0
	@if [ -f scripts/checks/generate_lifecycle_manifest.mjs ]; then \
		node scripts/checks/generate_lifecycle_manifest.mjs --proto-root "$(PROTO_PATH)" --manifest-out "docs/references/lifecycle/lifecycle_contract.json" --guide-out "docs/references/lifecycle/lifecycle_contract_guide.md"; \
	elif [ -f foundation/tooling/scripts/generate_lifecycle_manifest.mjs ]; then \
		node foundation/tooling/scripts/generate_lifecycle_manifest.mjs --proto-root "$(PROTO_PATH)" --manifest-out "docs/references/lifecycle/lifecycle_contract.json" --guide-out "docs/references/lifecycle/lifecycle_contract_guide.md"; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -f "$(FOUNDATION_PATH)/tooling/scripts/generate_lifecycle_manifest.mjs" ]; then \
		node "$(FOUNDATION_PATH)/tooling/scripts/generate_lifecycle_manifest.mjs" --proto-root "$(PROTO_PATH)" --manifest-out "docs/references/lifecycle/lifecycle_contract.json" --guide-out "docs/references/lifecycle/lifecycle_contract_guide.md"; \
	else \
		echo "Lifecycle manifest generator not found; skipping"; \
	fi

build: build-server build-frontend

dev:
	@if [ -f go.mod ]; then \
		go run ./cmd/server; \
	elif [ -f frontend/package.json ]; then \
		cd frontend && npm run dev; \
	else \
		echo "No runnable dev target found"; \
		exit 1; \
	fi

# run-local and dev-local boot the backend with no external services: the
# in-memory database driver (database.MemoryDB + Hermes hotplane) and the
# in-memory event bus replace Postgres and Redis. No Docker, no migrations, no
# containers — just `go run`. State is ephemeral (lost on restart), which is
# exactly what you want for fast test/verify and frontend development. Override
# the port with `make run-local PORT=9090`.
run-local:
	@if [ ! -f go.mod ]; then echo "No Go module; nothing to run"; exit 1; fi
	@mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"
	@echo "Starting in-memory backend on :$(or $(PORT),8080) (no Docker: memory DB + in-memory bus)"
	@APP_ENV=development STATE_STORE_DRIVER=memory EVENT_BUS_DRIVER=memory REQUIRE_AUTH=false \
		PORT=$(or $(PORT),8080) GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" \
		$(GOCMD) run ./cmd/server

dev-local:
	@if [ ! -f go.mod ]; then echo "No Go module; nothing to run"; exit 1; fi
	@mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"
	@echo "Starting in-memory backend + frontend dev (no Docker)"
	@APP_ENV=development STATE_STORE_DRIVER=memory EVENT_BUS_DRIVER=memory REQUIRE_AUTH=false \
		PORT=$(or $(PORT),8080) GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" \
		$(GOCMD) run ./cmd/server & \
		server_pid=$$!; \
		trap 'kill $$server_pid 2>/dev/null || true' EXIT INT TERM; \
		if [ -f frontend/package.json ]; then cd frontend && npm run dev; else wait $$server_pid; fi

build-server:
	@if [ -f go.mod ]; then \
		mkdir -p bin; \
		$(GOBUILD) -o bin/server ./cmd/server; \
	else \
		echo "No Go module; skipping server build"; \
	fi

docgen:
	@if [ -f go.mod ] && [ -d cmd/docgen ]; then \
		set -e; \
		mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"; \
		GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" $(GOCMD) run ./cmd/docgen > openapi.json; \
		echo "API documentation generated: openapi.json"; \
	else \
		echo "No docgen command; skipping OpenAPI generation"; \
	fi

build-frontend: frontend-prototype-runtime
	@if [ -f frontend/package.json ]; then \
		zsh $(FRONTEND_SCRIPT_RUNNER) . build; \
	else \
		echo "No frontend package; skipping frontend build"; \
	fi

build-runtime: runtime-bindings build-wasm build-rust-wasm wasm-manifest

runtime-bindings:
	@if [ -x foundation/runtime-sdk/scripts/generate_system_bindings.sh ]; then \
		foundation/runtime-sdk/scripts/generate_system_bindings.sh; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -x "$(FOUNDATION_PATH)/runtime-sdk/scripts/generate_system_bindings.sh" ]; then \
		"$(FOUNDATION_PATH)/runtime-sdk/scripts/generate_system_bindings.sh"; \
	else \
		echo "Runtime SDK binding generator not found; skipping"; \
	fi

build-wasm:
	@if [ -d wasm ]; then \
		echo "Building WASM..."; \
		mkdir -p "$(WASM_PUBLIC_DIR)"; \
		GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" GOOS=js GOARCH=wasm $(GOBUILD) -ldflags="-s -w" -o "$(WASM_PUBLIC_DIR)/main.wasm" ./wasm; \
		goroot=$$(go env GOROOT); \
		if [ -f "$$goroot/lib/wasm/wasm_exec.js" ]; then cp "$$goroot/lib/wasm/wasm_exec.js" "$(WASM_PUBLIC_DIR)/wasm_exec.js"; \
		elif [ -f "$$goroot/misc/wasm/wasm_exec.js" ]; then cp "$$goroot/misc/wasm/wasm_exec.js" "$(WASM_PUBLIC_DIR)/wasm_exec.js"; fi; \
		if command -v "$(WASM_OPT)" >/dev/null 2>&1; then tmp="$(WASM_PUBLIC_DIR)/main.wasm.opt"; if "$(WASM_OPT)" $(WASM_OPT_FLAGS) -o "$$tmp" "$(WASM_PUBLIC_DIR)/main.wasm"; then mv "$$tmp" "$(WASM_PUBLIC_DIR)/main.wasm"; else rm -f "$$tmp"; echo "wasm-opt failed; keeping unoptimized main.wasm"; fi; fi; \
		if command -v "$(BROTLI)" >/dev/null 2>&1; then "$(BROTLI)" -f "$(WASM_PUBLIC_DIR)/main.wasm"; fi; \
	fi
	@$(MAKE) wasm-manifest

build-wasm-dev:
	@if [ -d wasm ]; then \
		echo "Building WASM (development)..."; \
		mkdir -p "$(WASM_PUBLIC_DIR)"; \
		GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" GOOS=js GOARCH=wasm $(GOBUILD) -o "$(WASM_PUBLIC_DIR)/main.wasm" ./wasm; \
		goroot=$$(go env GOROOT); \
		if [ -f "$$goroot/lib/wasm/wasm_exec.js" ]; then cp "$$goroot/lib/wasm/wasm_exec.js" "$(WASM_PUBLIC_DIR)/wasm_exec.js"; \
		elif [ -f "$$goroot/misc/wasm/wasm_exec.js" ]; then cp "$$goroot/misc/wasm/wasm_exec.js" "$(WASM_PUBLIC_DIR)/wasm_exec.js"; fi; \
	fi
	@$(MAKE) wasm-manifest

build-rust-wasm:
	@if [ -f "$(RUST_WASM_DIR)/Cargo.toml" ]; then \
		echo "Building Rust WASM modules..."; \
		mkdir -p "$(WASM_MODULE_DIR)"; \
		if [ "$(RUST_WASM_PROFILE)" = "release" ]; then \
			cargo build --manifest-path "$(RUST_WASM_DIR)/Cargo.toml" --target "$(RUST_TARGET)" --release; \
			src_dir="$(RUST_WASM_DIR)/target/$(RUST_TARGET)/release"; \
		else \
			cargo build --manifest-path "$(RUST_WASM_DIR)/Cargo.toml" --target "$(RUST_TARGET)"; \
			src_dir="$(RUST_WASM_DIR)/target/$(RUST_TARGET)/debug"; \
		fi; \
		if ls "$$src_dir"/*.wasm >/dev/null 2>&1; then \
			cp "$$src_dir"/*.wasm "$(WASM_MODULE_DIR)/"; \
			if command -v "$(WASM_OPT)" >/dev/null 2>&1; then for wasm in "$(WASM_MODULE_DIR)"/*.wasm; do tmp="$$wasm.opt"; if "$(WASM_OPT)" $(WASM_OPT_FLAGS) -o "$$tmp" "$$wasm"; then mv "$$tmp" "$$wasm"; else rm -f "$$tmp"; echo "wasm-opt failed; keeping unoptimized $$wasm"; fi; done; fi; \
			if command -v "$(BROTLI)" >/dev/null 2>&1; then for wasm in "$(WASM_MODULE_DIR)"/*.wasm; do "$(BROTLI)" -f "$$wasm"; done; fi; \
		fi; \
	else \
		echo "No $(RUST_WASM_DIR)/Cargo.toml; skipping Rust WASM modules"; \
	fi
	@$(MAKE) wasm-manifest

wasm-manifest:
	@if [ -f scripts/checks/wasm_manifest.mjs ]; then \
		node scripts/checks/wasm_manifest.mjs --public-dir "$(WASM_PUBLIC_DIR)" --out "$(WASM_MANIFEST)"; \
	elif [ -f foundation/tooling/scripts/wasm_manifest.mjs ]; then \
		node foundation/tooling/scripts/wasm_manifest.mjs --public-dir "$(WASM_PUBLIC_DIR)" --out "$(WASM_MANIFEST)"; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -f "$(FOUNDATION_PATH)/tooling/scripts/wasm_manifest.mjs" ]; then \
		node "$(FOUNDATION_PATH)/tooling/scripts/wasm_manifest.mjs" --public-dir "$(WASM_PUBLIC_DIR)" --out "$(WASM_MANIFEST)"; \
	else \
		echo "WASM manifest generator not found; skipping"; \
	fi

native-dev:
	@if [ -f "$(NATIVE_DIR)/package.json" ]; then \
		cd "$(NATIVE_DIR)" && npm run dev; \
	else \
		echo "No native package; skipping native dev"; \
	fi

native-build: build-runtime build-frontend
	@if [ -f "$(NATIVE_DIR)/package.json" ]; then \
		cd "$(NATIVE_DIR)" && npm run build; \
	else \
		echo "No native package; skipping native build"; \
	fi

native-mobile-init:
	@if [ -f "$(NATIVE_DIR)/package.json" ]; then \
		cd "$(NATIVE_DIR)" && npm run mobile:init; \
	else \
		echo "No native package; skipping mobile init"; \
	fi

native-bench:
	@if [ -x scripts/checks/native_benchmark.sh ]; then \
		scripts/checks/native_benchmark.sh .; \
	elif [ -x foundation/tooling/scripts/native_benchmark.sh ]; then \
		foundation/tooling/scripts/native_benchmark.sh .; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -x "$(FOUNDATION_PATH)/tooling/scripts/native_benchmark.sh" ]; then \
		"$(FOUNDATION_PATH)/tooling/scripts/native_benchmark.sh" .; \
	elif [ -f "$(NATIVE_DIR)/package.json" ]; then \
		cd "$(NATIVE_DIR)" && npm run bench; \
	else \
		echo "No native benchmark target found; skipping"; \
	fi

native-doctor:
	@if [ -f "$(NATIVE_DIR)/package.json" ]; then \
		cd "$(NATIVE_DIR)" && npm run doctor; \
	else \
		echo "No native package; skipping native doctor"; \
	fi

delivery-metrics:
	@if [ -f scripts/checks/ci_delivery_metrics.mjs ]; then \
		node scripts/checks/ci_delivery_metrics.mjs --out delivery-metrics/local-event.json; \
	elif [ -f foundation/tooling/scripts/ci_delivery_metrics.mjs ]; then \
		node foundation/tooling/scripts/ci_delivery_metrics.mjs --out delivery-metrics/local-event.json; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -f "$(FOUNDATION_PATH)/tooling/scripts/ci_delivery_metrics.mjs" ]; then \
		node "$(FOUNDATION_PATH)/tooling/scripts/ci_delivery_metrics.mjs" --out delivery-metrics/local-event.json; \
	else \
		echo "Delivery metrics collector not found; skipping"; \
	fi

test: test-unit test-frontend

# Explicit concurrency gates (runtime performance contract): test-unit already
# runs -race, but these named targets keep the race and Loom evidence lanes
# individually invocable and verify-enforced.
test-go-race:
	@echo "Running Go tests under -race (concurrency gate)..."
	@if [ -f go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"; \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go test -race -count=1 -timeout $(GO_TEST_TIMEOUT) $$packages; \
	elif [ -f backend/go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"; \
		cd backend && \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go test -race -count=1 -timeout $(GO_TEST_TIMEOUT) $$packages; \
	else \
		echo "No Go module; skipping race gate"; \
	fi

test-rust-loom:
	@echo "Running vendored runtime-sdk Loom interleaving model tests..."
	@if [ -f foundation/runtime-sdk/rust/Cargo.toml ]; then \
		cargo test --manifest-path foundation/runtime-sdk/rust/Cargo.toml -p ovrt-core --features loom loom_verification; \
	else \
		echo "No vendored runtime-sdk; skipping Loom gate"; \
	fi

test-local-full: communication-contracts test-unit test-wasm test-frontend test-integration test-e2e
	@echo "Local full test path complete"

test-local-services: test-local-full test-load test-bench
	@echo "Opt-in local service evidence path complete"

test-wasm: build-runtime
	@echo "Runtime and WASM artifacts are current"

test-e2e: communication-contracts
	@if [ -f frontend/package.json ]; then \
		if grep -Eq '"e2e"[[:space:]]*:' frontend/package.json; then \
			zsh $(FRONTEND_SCRIPT_RUNNER) . e2e; \
		else \
			zsh $(FRONTEND_SCRIPT_RUNNER) . test:e2e; \
		fi; \
	else \
		echo "No frontend package; skipping frontend e2e"; \
	fi

test-unit:
	@if [ -f go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"; \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" $(GOTEST) -race -count=1 -timeout $(GO_TEST_TIMEOUT) $$packages; \
	elif [ -f backend/go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"; \
		cd backend && \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No backend Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" $(GOTEST) -race -count=1 -timeout $(GO_TEST_TIMEOUT) $$packages; \
	else \
		echo "No Go module; skipping Go tests"; \
	fi

test-coverage:
	@set -e; \
	root_dir="$$(pwd)"; \
	go_dir="."; \
	if [ ! -f go.mod ] && [ -f backend/go.mod ]; then go_dir="backend"; fi; \
	if [ ! -f "$$go_dir/go.mod" ]; then \
		echo "No Go module; skipping Go coverage"; \
		exit 0; \
	fi; \
	mkdir -p coverage "$(GO_CACHE_DIR)"; \
	( cd "$$go_dir" && \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" $(GOTEST) -race -count=1 -covermode=atomic -coverprofile="$$root_dir/coverage/go.raw.out" -timeout $(GO_TEST_TIMEOUT) $$packages; \
	); \
	awk 'NR == 1 { print; next } $$0 !~ /\/generated\// && $$0 !~ /\.pb\.go:/' coverage/go.raw.out > coverage/go.owned.out; \
	go tool cover -func=coverage/go.owned.out | tee coverage/go.summary.txt; \
	coverage=$$(awk '/^total:/ { gsub("%", "", $$3); print $$3 }' coverage/go.summary.txt); \
	awk -v coverage="$$coverage" -v threshold="$(GO_COVERAGE_THRESHOLD)" 'BEGIN { if (coverage + 0 < threshold + 0) { printf("coverage gate failed: %.1f%% < %.1f%%\n", coverage, threshold) > "/dev/stderr"; exit 1 } }'

test-integration:
	@set -e; \
	go_dir="."; \
	test_pkg="./tests/integration/..."; \
	if [ ! -f go.mod ] && [ -f backend/go.mod ]; then \
		go_dir="backend"; \
		test_pkg="./tests/integration/..."; \
	fi; \
	if [ ! -f "$$go_dir/go.mod" ]; then \
		echo "No Go module; skipping integration tests"; \
		exit 0; \
	fi; \
	if [ ! -d "$$go_dir/tests/integration" ]; then \
		echo "No tests/integration package; skipping integration tests"; \
		exit 0; \
	fi; \
	started_test_env=0; \
	if [ -z "$$CI" ]; then \
		$(MAKE) test-env-up; \
		started_test_env=1; \
		trap 'if [ "$$started_test_env" = "1" ]; then $(MAKE) test-env-down >/dev/null 2>&1 || true; fi' EXIT; \
	fi; \
	$(MAKE) migrate-test; \
	test_db_host="$(TEST_DB_HOST)"; \
	test_db_port="$(TEST_DB_PORT)"; \
	test_database_url="$(TEST_DATABASE_URL)"; \
	test_redis_url="$(TEST_REDIS_URL)"; \
	if [ -f "$(TEST_ENV_FILE)" ]; then \
		. "$(TEST_ENV_FILE)"; \
		test_db_host="$$TEST_DB_HOST"; \
		test_db_port="$$TEST_DB_PORT"; \
		test_database_url="$$TEST_DATABASE_URL"; \
		test_redis_url="$$TEST_REDIS_URL"; \
	fi; \
	mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"; \
	APP_ENV=test \
	GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" \
	TEST_INFRA_REQUIRED=true \
	TEST_REDIS_REQUIRED=true \
	TEST_DB_HOST="$$test_db_host" \
	TEST_DB_PORT="$$test_db_port" \
	TEST_DB_USER="$(TEST_DB_USER)" \
	TEST_DB_PASSWORD="$(TEST_DB_PASSWORD)" \
	TEST_DB_NAME="$(TEST_DB_NAME)" \
	TEST_DATABASE_URL="$$test_database_url" \
	DATABASE_URL="$$test_database_url" \
	TEST_REDIS_URL="$$test_redis_url" \
	REDIS_URL="$$test_redis_url" \
	REDIS_PREFIX="test_$(PROJECT_NAME):" \
	$(GOTEST) -C "$$go_dir" -race -count=1 -timeout $(GO_TEST_TIMEOUT) -tags=integration "$$test_pkg"

test-load:
	@set -e; \
	go_dir="."; \
	load_pkg="./tests/load/..."; \
	if [ ! -f go.mod ] && [ -f backend/go.mod ]; then \
		go_dir="backend"; \
		load_pkg="./tests/load/..."; \
	fi; \
	if [ ! -f "$$go_dir/go.mod" ]; then \
		echo "No Go module; skipping load tests"; \
		exit 0; \
	fi; \
	if [ ! -d "$$go_dir/tests/load" ]; then \
		echo "No tests/load package; skipping load tests"; \
		exit 0; \
	fi; \
	started_test_env=0; \
	if [ -z "$$CI" ]; then \
		$(MAKE) test-env-up; \
		started_test_env=1; \
		trap 'if [ "$$started_test_env" = "1" ]; then $(MAKE) test-env-down >/dev/null 2>&1 || true; fi' EXIT; \
	fi; \
	$(MAKE) migrate-test; \
	test_db_host="$(TEST_DB_HOST)"; \
	test_db_port="$(TEST_DB_PORT)"; \
	test_database_url="$(TEST_DATABASE_URL)"; \
	test_redis_url="$(TEST_REDIS_URL)"; \
	if [ -f "$(TEST_ENV_FILE)" ]; then \
		. "$(TEST_ENV_FILE)"; \
		test_db_host="$$TEST_DB_HOST"; \
		test_db_port="$$TEST_DB_PORT"; \
		test_database_url="$$TEST_DATABASE_URL"; \
		test_redis_url="$$TEST_REDIS_URL"; \
	fi; \
	mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"; \
	APP_ENV=test \
	GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" \
	RUN_LOAD_TESTS="$${RUN_LOAD_TESTS:-1}" \
	TEST_INFRA_REQUIRED=true \
	TEST_REDIS_REQUIRED=true \
	TEST_DB_HOST="$$test_db_host" \
	TEST_DB_PORT="$$test_db_port" \
	TEST_DB_USER="$(TEST_DB_USER)" \
	TEST_DB_PASSWORD="$(TEST_DB_PASSWORD)" \
	TEST_DB_NAME="$(TEST_DB_NAME)" \
	TEST_DATABASE_URL="$$test_database_url" \
	DATABASE_URL="$$test_database_url" \
	TEST_REDIS_URL="$$test_redis_url" \
	REDIS_URL="$$test_redis_url" \
	REDIS_PREFIX="test_$(PROJECT_NAME):" \
	$(GOTEST) -C "$$go_dir" $(LOAD_RACE_FLAG) -v -count=1 -timeout "$${LOAD_TEST_TIMEOUT:-10m}" "$$load_pkg"

test-bench:
	@if [ -f go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"; \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" $(GOTEST) -run '^$$' -bench "$${BENCH_PATTERN:-.}" -benchmem -count "$${BENCH_COUNT:-3}" -timeout "$${BENCH_TIMEOUT:-20m}" $$packages; \
	elif [ -f backend/go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"; \
		cd backend && \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No backend Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" $(GOTEST) -run '^$$' -bench "$${BENCH_PATTERN:-.}" -benchmem -count "$${BENCH_COUNT:-3}" -timeout "$${BENCH_TIMEOUT:-20m}" $$packages; \
	else \
		echo "No Go module; skipping benchmarks"; \
	fi
	@$(MAKE) --no-print-directory test-bench-frontend

test-bench-frontend: frontend-prototype-runtime
	@if [ -x scripts/checks/frontend_workbench_profile.sh ]; then \
		scripts/checks/frontend_workbench_profile.sh .; \
	elif [ -x foundation/tooling/scripts/frontend_workbench_profile.sh ]; then \
		foundation/tooling/scripts/frontend_workbench_profile.sh .; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -x "$(FOUNDATION_PATH)/tooling/scripts/frontend_workbench_profile.sh" ]; then \
		"$(FOUNDATION_PATH)/tooling/scripts/frontend_workbench_profile.sh" .; \
	else \
		echo "No frontend workbench profile target found; skipping"; \
	fi

test-all: lint-foundation test-unit test-integration test-frontend

test-frontend: frontend-prototype-runtime
	@if [ -f frontend/package.json ]; then \
		FOUNDATION_VITEST_WORKERS="$(FOUNDATION_VITEST_WORKERS)" FOUNDATION_VITEST_SERIAL="$(FOUNDATION_VITEST_SERIAL)" zsh $(FRONTEND_SCRIPT_RUNNER) . test; \
	else \
		echo "No frontend package; skipping frontend tests"; \
	fi

test-env-up:
	@if [ ! -f docker-compose.test.yml ]; then \
		echo "docker-compose.test.yml not found"; \
		exit 1; \
	fi
	@$(DOCKER_COMPOSE_TEST) down -v --remove-orphans >/dev/null 2>&1 || true
	@rm -f "$(TEST_ENV_FILE)"
	@TEST_DB_USER="$(TEST_DB_USER)" \
	TEST_DB_PASSWORD="$(TEST_DB_PASSWORD)" \
	TEST_DB_NAME="$(TEST_DB_NAME)" \
	TEST_DB_PORT="$(TEST_DB_PORT)" \
	POSTGRES_VERSION="$(POSTGRES_VERSION)" \
	TEST_POSTGRES_IMAGE="$(TEST_POSTGRES_IMAGE)" \
	TEST_POSTGRES_PLATFORM="$(TEST_POSTGRES_PLATFORM)" \
	TEST_REDIS_PORT="$(TEST_REDIS_PORT)" \
	TEST_REDIS_PASSWORD="$(TEST_REDIS_PASSWORD)" \
	$(DOCKER_COMPOSE_TEST) up -d test-postgres test-redis
	@mkdir -p "$$(dirname "$(TEST_ENV_FILE)")"
	@db_port="$$($(DOCKER_COMPOSE_TEST) port test-postgres 5432 | awk -F: 'END { print $$NF }')"; \
	redis_port="$$($(DOCKER_COMPOSE_TEST) port test-redis 6379 | awk -F: 'END { print $$NF }')"; \
	if [ -z "$$db_port" ] || [ -z "$$redis_port" ]; then \
		echo "failed to resolve test Docker host ports"; \
		exit 1; \
	fi; \
	db_url="postgresql://$(TEST_DB_USER):$(TEST_DB_PASSWORD)@127.0.0.1:$$db_port/$(TEST_DB_NAME)?sslmode=disable"; \
	if [ -n "$(TEST_REDIS_PASSWORD)" ]; then \
		redis_url="redis://:$(TEST_REDIS_PASSWORD)@127.0.0.1:$$redis_port/$(TEST_REDIS_DB)"; \
	else \
		redis_url="redis://127.0.0.1:$$redis_port/$(TEST_REDIS_DB)"; \
	fi; \
	{ \
		echo "TEST_DB_HOST=127.0.0.1"; \
		echo "TEST_DB_PORT=$$db_port"; \
		echo "TEST_DATABASE_URL=$$db_url"; \
		echo "DATABASE_URL=$$db_url"; \
		echo "TEST_REDIS_HOST=127.0.0.1"; \
		echo "TEST_REDIS_PORT=$$redis_port"; \
		echo "TEST_REDIS_URL=$$redis_url"; \
		echo "REDIS_URL=$$redis_url"; \
	} > "$(TEST_ENV_FILE)"

test-env-down:
	@if [ -f docker-compose.test.yml ]; then \
		$(DOCKER_COMPOSE_TEST) down -v --remove-orphans; \
	fi
	@rm -f "$(TEST_ENV_FILE)"

test-env-logs:
	@if [ -f docker-compose.test.yml ]; then \
		$(DOCKER_COMPOSE_TEST) logs -f test-postgres test-redis; \
	fi

migrate-test:
	@echo "Running test migrations..."
	@set -e; \
	if [ -f docker-compose.test.yml ] && [ -z "$$CI" ]; then \
		attempt=1; \
		while true; do \
			if TEST_DB_USER="$(TEST_DB_USER)" \
				TEST_DB_PASSWORD="$(TEST_DB_PASSWORD)" \
				TEST_DB_NAME="$(TEST_DB_NAME)" \
				POSTGRES_VERSION="$(POSTGRES_VERSION)" \
				TEST_POSTGRES_IMAGE="$(TEST_POSTGRES_IMAGE)" \
				TEST_POSTGRES_PLATFORM="$(TEST_POSTGRES_PLATFORM)" \
				$(DOCKER_COMPOSE_TEST) run --rm test-migrate; then \
				break; \
			fi; \
			status=$$?; \
			if [ "$$attempt" -ge "$(MIGRATE_TEST_MAX_RETRIES)" ]; then \
				exit "$$status"; \
			fi; \
			echo "Retrying test migrations... ($$attempt/$(MIGRATE_TEST_MAX_RETRIES))"; \
			attempt=$$((attempt + 1)); \
			sleep "$(MIGRATE_TEST_RETRY_SLEEP)"; \
		done; \
	elif command -v migrate >/dev/null 2>&1; then \
		migrate -path migrations -database "$(TEST_DATABASE_URL)" up; \
	else \
		echo "Install golang-migrate or run make test-env-up locally"; \
		exit 1; \
	fi

migrate-test-down:
	@if command -v migrate >/dev/null 2>&1; then \
		migrate -path migrations -database "$(TEST_DATABASE_URL)" down 1; \
	else \
		echo "Install golang-migrate to roll back test migrations"; \
		exit 1; \
	fi

migrate-test-reset:
	@if command -v migrate >/dev/null 2>&1; then \
		migrate -path migrations -database "$(TEST_DATABASE_URL)" drop -f; \
		migrate -path migrations -database "$(TEST_DATABASE_URL)" up; \
	else \
		$(MAKE) test-env-down; \
		$(MAKE) test-env-up; \
		$(MAKE) migrate-test; \
	fi

lint: lint-go lint-rust lint-frontend lint-foundation

lint-go:
	@if [ -f go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)" "$(GO_MOD_CACHE_DIR)"; \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go list -f '{{.Dir}}' ./... | sed "s#^$$(pwd)#.#" | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		if command -v golangci-lint >/dev/null 2>&1; then \
			GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" golangci-lint run --timeout 10m $$packages; \
		else \
			GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" go vet $$packages; \
		fi; \
	fi

lint-rust:
	@$(MAKE) --no-print-directory check-rust-static-analysis
	@$(MAKE) --no-print-directory check-rust-runtime-practices

check-rust:
	@if [ -x scripts/checks/check-rust.sh ]; then \
		scripts/checks/check-rust.sh .; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -x "$(FOUNDATION_PATH)/scripts/check-rust.sh" ]; then \
		"$(FOUNDATION_PATH)/scripts/check-rust.sh" .; \
	else \
		$(MAKE) --no-print-directory check-rust-static-analysis; \
		$(MAKE) --no-print-directory check-rust-runtime-practices; \
	fi

lint-frontend: frontend-prototype-runtime
	@if [ -f frontend/package.json ]; then \
		zsh $(FRONTEND_SCRIPT_RUNNER) . lint; \
	else \
		echo "No frontend package; skipping frontend lint"; \
	fi

FOUNDATION_LINT_CHECKS := check-doc-references check-markdown-frontmatter check-agent-contract check-practice-controls check-runtime-performance-contracts check-formal-methods check-operational-excellence check-go-fix check-go-static-analysis check-rust-static-analysis check-rust-runtime-practices check-coding-practices check-testing-practices check-go-concurrency-practices check-logging-practices check-metadata-practices check-dynamic-payload-practices check-database-practices check-atomic-lane-purity check-redis-practices check-river-practices check-migration-structure check-lifecycle-manifest check-app-security-profile check-domain-contract-consistency check-contract-drift check-server-kit-module-contract check-server-kit-usage check-project-scaffold
FOUNDATION_LINT_CHECK_TIMEOUT_SEC ?= 600

lint-foundation:
	@tmp_log=$$(mktemp "$${TMPDIR:-/tmp}/foundation-lint.XXXXXX"); \
	trap 'rm -f "$$tmp_log"' EXIT; \
	runner="./scripts/checks/foundation_lint_check_runner.sh"; \
	for check in $(FOUNDATION_LINT_CHECKS); do \
		printf '[RUN] %s\n' "$$check"; \
		if FOUNDATION_LINT_CHECK_TIMEOUT_SEC="$(FOUNDATION_LINT_CHECK_TIMEOUT_SEC)" zsh "$$runner" "$$tmp_log" "$(MAKE)" --no-print-directory "$$check"; then \
			printf '[OK] %s\n' "$$check"; \
			: >"$$tmp_log"; \
		else \
			cat "$$tmp_log"; \
			exit 1; \
		fi; \
	done; \
	echo "foundation lint passed"

check-go-fix:
	@zsh ./scripts/checks/go_fix_check.sh .

check-go-static-analysis:
	@zsh ./scripts/checks/go_static_analysis_check.sh .

check-rust-static-analysis:
	@if [ -x scripts/checks/rust_static_analysis_check.sh ]; then \
		zsh scripts/checks/rust_static_analysis_check.sh .; \
	elif [ -x foundation/tooling/scripts/rust_static_analysis_check.sh ]; then \
		zsh foundation/tooling/scripts/rust_static_analysis_check.sh .; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -x "$(FOUNDATION_PATH)/tooling/scripts/rust_static_analysis_check.sh" ]; then \
		zsh "$(FOUNDATION_PATH)/tooling/scripts/rust_static_analysis_check.sh" .; \
	else \
		echo "Rust static analysis check script not found; skipping"; \
	fi

check-rust-runtime-practices:
	@if [ -x scripts/checks/rust_runtime_practices_check.sh ]; then \
		zsh scripts/checks/rust_runtime_practices_check.sh .; \
	elif [ -x foundation/tooling/scripts/rust_runtime_practices_check.sh ]; then \
		zsh foundation/tooling/scripts/rust_runtime_practices_check.sh .; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -x "$(FOUNDATION_PATH)/tooling/scripts/rust_runtime_practices_check.sh" ]; then \
		zsh "$(FOUNDATION_PATH)/tooling/scripts/rust_runtime_practices_check.sh" .; \
	else \
		echo "Rust runtime practices check script not found; skipping"; \
	fi

check-agent-contract:
	@zsh ./scripts/checks/agent_contract_check.sh .

check-doc-references:
	@node ./scripts/checks/docs_reference_check.mjs .

check-markdown-frontmatter:
	@node ./scripts/checks/markdown_frontmatter_check.mjs .

check-practice-controls:
	@zsh ./scripts/checks/practice_controls_check.sh .

check-runtime-performance-contracts:
	@zsh ./scripts/checks/runtime_performance_contract_check.sh .

check-formal-methods:
	@zsh ./scripts/checks/formal_methods_check.sh .

check-operational-excellence:
	@zsh ./scripts/checks/operational_excellence_check.sh .

check-coding-practices:
	@zsh ./scripts/checks/coding_practices_check.sh .

check-testing-practices:
	@zsh ./scripts/checks/testing_practices_check.sh .

check-go-concurrency-practices:
	@bash ./scripts/checks/go_concurrency_practices_check.sh .

check-logging-practices:
	@bash ./scripts/checks/logging_practices_check.sh .

check-metadata-practices:
	@zsh ./scripts/checks/metadata_practices_check.sh .

check-dynamic-payload-practices:
	@zsh ./scripts/checks/dynamic_payload_practices_check.sh .

check-database-practices:
	@zsh ./scripts/checks/database_practices_check.sh .

check-atomic-lane-purity:
	@zsh ./scripts/checks/atomic_lane_purity_check.sh .

check-redis-practices:
	@zsh ./scripts/checks/redis_practices_check.sh .

check-river-practices:
	@zsh ./scripts/checks/river_practices_check.sh .

check-migration-structure:
	@zsh ./scripts/checks/migration_structure_check.sh .

check-lifecycle-manifest:
	@zsh ./scripts/checks/check_lifecycle_manifest.sh .

check-app-security-profile:
	@zsh ./scripts/checks/app_security_profile_check.sh .

check-domain-contract-consistency:
	@zsh ./scripts/checks/domain_contract_consistency_check.sh .

check-contract-drift:
	@zsh ./scripts/checks/contract_drift_check.sh .

check-server-kit-module-contract:
	@bash ./scripts/checks/server_kit_module_contract_check.sh .

check-server-kit-usage:
	@zsh ./scripts/checks/server_kit_usage_check.sh .

check-project-scaffold:
	@zsh ./scripts/checks/project_scaffold_check.sh .

verify: lint test test-go-race build build-wasm
	@echo "Verification complete"

docker-build:
	@if [ -f docker-compose.yml ]; then DOCKER_BUILDKIT=$(DOCKER_BUILDKIT) DOCKER_CACHE_NAMESPACE="$(DOCKER_CACHE_NAMESPACE)" $(DOCKER_COMPOSE) build $(DOCKER_BUILD_ARGS); fi

docker-up:
	@if [ -f docker-compose.yml ]; then $(DOCKER_COMPOSE) up -d; fi

docker-down:
	@if [ -f docker-compose.yml ]; then $(DOCKER_COMPOSE) down; fi

docker-logs:
	@if [ -f docker-compose.yml ]; then $(DOCKER_COMPOSE) logs -f; fi

migrate-up:
	@echo "Running migrations..."
	@if command -v migrate >/dev/null 2>&1 && [ -n "$$DATABASE_URL" ]; then \
		migrate -path migrations -database "$$DATABASE_URL" up; \
	else \
		echo "Install golang-migrate and set DATABASE_URL"; \
		exit 1; \
	fi

migrate-down:
	@echo "Rolling back migrations..."
	@if command -v migrate >/dev/null 2>&1 && [ -n "$$DATABASE_URL" ]; then \
		migrate -path migrations -database "$$DATABASE_URL" down 1; \
	else \
		echo "Install golang-migrate and set DATABASE_URL"; \
		exit 1; \
	fi

migrate-create:
	@echo "Creating new migration..."
	@read -p "Migration name: " name; \
	seq=$$(printf "%06d" $$(($$(find migrations -maxdepth 1 -name '*.up.sql' | wc -l) + 1))); \
	touch migrations/$${seq}_$${name}.up.sql; \
	touch migrations/$${seq}_$${name}.down.sql

foundation-update:
	@if [ -n "$(FOUNDATION_PATH)" ] && [ -x "$(FOUNDATION_PATH)/scripts/update-project.sh" ]; then \
		"$(FOUNDATION_PATH)/scripts/update-project.sh" "$(CURDIR)" --force; \
	else \
		echo "Foundation updater not found. Run from the canonical foundation repo."; \
	fi

foundation-status:
	@cat .foundation

clean:
	rm -rf bin coverage dist coverage.out coverage.html .cache/go-build
	@rm -rf frontend/dist frontend/coverage
	@rm -f "$(WASM_PUBLIC_DIR)/main.wasm" "$(WASM_PUBLIC_DIR)/main.wasm.br" "$(WASM_PUBLIC_DIR)/kernel.wasm" "$(WASM_PUBLIC_DIR)/kernel.wasm.br" "$(WASM_PUBLIC_DIR)/wasm_exec.js" "$(WASM_MANIFEST)"
	@rm -rf "$(WASM_MODULE_DIR)"
	@find $(PROTO_PATH) -name "*.pb.go" -delete 2>/dev/null || true
	@find $(PROTO_PATH) -name "*_grpc.pb.go" -delete 2>/dev/null || true

help:
	@echo "Development:"
	@echo "  setup            - Install tools and dependencies"
	@echo "  dev              - Start the development server with your own runner"
	@echo "  run-local        - Run backend fully in-memory (no Docker: memory DB + bus)"
	@echo "  dev-local        - Run in-memory backend + frontend dev together (no Docker)"
	@echo "  build            - Build backend and frontend"
	@echo "  build-runtime    - Generate runtime bindings, build WASM artifacts, and emit runtime manifest"
	@echo "  test             - Run unit and frontend tests"
	@echo "  test-local-full  - Regenerate contracts, run backend/frontend/runtime/WASM tests, and run DB/Redis/Hermes integration"
	@echo "  test-local-services - Run local full path plus opt-in load and benchmark evidence"
	@echo "  test-wasm        - Generate runtime bindings, build WASM artifacts, and emit runtime manifest"
	@echo "  test-e2e         - Run optional frontend e2e script when declared"
	@echo "  test-unit        - Run Go tests without integration tag"
	@echo "  test-integration - Start scaffolded DB/Redis, migrate, and run integration tests"
	@echo "  test-load        - Run opt-in DB/Redis/queue recurring load tests"
	@echo "  test-bench       - Run Go benchmarks plus frontend workbench profile where available"
	@echo "  test-bench-frontend - Run frontend workbench allocation profile"
	@echo "  test-all         - Run foundation checks, unit tests, integration tests, and frontend tests"
	@echo "  lint             - Run linters and scaffold checks"
	@echo "  check-rust       - Run Rust fmt, clippy, runtime practices, and tests"
	@echo "  verify           - Run lint, test, and build"
	@echo ""
	@echo "Foundation:"
	@echo "  foundation-update - Sync project-managed scaffold from foundation"
	@echo "  foundation-status - Print foundation metadata"
	@echo "  runtime-bindings  - Regenerate runtime-sdk buffer bindings"
	@echo "  lifecycle-contracts - Generate proto-derived VerifyCommandLifecycle tests"
	@echo "  lifecycle-manifest - Generate machine-readable lifecycle event/review vectors"
	@echo "  frontend-prototype-runtime - Generate proto-derived frontend prototype stores, hooks, fixtures, and benchmarks"
	@echo "  wasm-manifest     - Emit frontend runtime/WASM artifact manifest"
	@echo "  native-dev        - Start the Tauri native shell against the frontend dev server"
	@echo "  native-build      - Build runtime, frontend, and native shell"
	@echo "  native-bench      - Run report-only native/runtime benchmark slices"
	@echo "  native-mobile-init - Initialize Tauri Android and iOS projects"
	@echo "  native-doctor     - Print native/Tauri environment diagnostics"
	@echo "  delivery-metrics  - Emit a local DORA/incident collection event"
	@echo ""
	@echo "Checks:"
	@echo "  check-doc-references"
	@echo "  check-markdown-frontmatter"
	@echo "  check-agent-contract"
	@echo "  check-practice-controls"
	@echo "  check-runtime-performance-contracts"
	@echo "  check-formal-methods"
	@echo "  check-operational-excellence"
	@echo "  check-rust-static-analysis"
	@echo "  check-rust-runtime-practices"
	@echo "  check-coding-practices"
	@echo "  check-logging-practices"
	@echo "  check-dynamic-payload-practices"
	@echo "  check-database-practices"
	@echo "  check-atomic-lane-purity"
	@echo "  check-redis-practices"
	@echo "  check-river-practices"
	@echo "  check-migration-structure"
	@echo "  check-lifecycle-manifest"
	@echo "  check-app-security-profile"
	@echo "  check-domain-contract-consistency"
	@echo "  check-contract-drift"
	@echo "  check-project-scaffold"
	@echo ""
	@echo "Docker & DB:"
	@echo "  docker-up        - Start development containers"
	@echo "  docker-down      - Stop development containers"
	@echo "  docker-logs      - Follow development container logs"
	@echo "  docker-build     - Build images with shared BuildKit dependency caches"
	@echo "  test-env-up      - Start isolated test PostgreSQL and Redis"
	@echo "  test-env-down    - Stop isolated test infrastructure and remove volumes"
	@echo "  migrate-up       - Run database migrations against DATABASE_URL"
	@echo "  migrate-test     - Run migrations against scaffolded test database"
	@echo "  migrate-create   - Create paired migration files"
