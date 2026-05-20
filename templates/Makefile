.PHONY: all setup deps install-tools dev build build-server build-frontend build-runtime communication-contracts lifecycle-contracts runtime-bindings foundation-transport-proto build-wasm build-wasm-dev build-rust-wasm wasm-manifest native-dev native-build native-mobile-init native-bench native-doctor delivery-metrics test test-unit test-coverage test-integration test-load test-bench test-all test-frontend test-env-up test-env-down test-env-logs migrate-test migrate-test-down migrate-test-reset lint lint-go lint-rust lint-frontend lint-foundation verify clean help proto proto-ts docker-build docker-up docker-down docker-logs migrate-up migrate-down migrate-create foundation-update foundation-status check-go-fix check-go-static-analysis check-coding-practices check-testing-practices check-metadata-practices check-database-practices check-redis-practices check-river-practices check-migration-structure check-domain-contract-consistency check-contract-drift check-server-kit-usage check-project-scaffold

PROJECT_NAME := $(shell basename "$(CURDIR)" | sed 's/_v[0-9]*$$//')
GO_MODULE := $(shell grep -m1 "^module " go.mod 2>/dev/null | cut -d' ' -f2)
FOUNDATION_PATH := $(shell grep -m1 "^FOUNDATION_PATH=" .foundation 2>/dev/null | cut -d'=' -f2 | sed 's/\\ / /g')
PROTO_PATH := api/protos
WASM_PUBLIC_DIR ?= frontend/public
WASM_MODULE_DIR ?= $(WASM_PUBLIC_DIR)/modules
WASM_MANIFEST ?= $(WASM_PUBLIC_DIR)/runtime/wasm-manifest.json
WASM_OPT ?= wasm-opt
WASM_OPT_FLAGS ?= -Oz --enable-bulk-memory-opt
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
GO_PACKAGE_EXCLUDE_RE ?= /node_modules/|/api/protos/
GO_COVERAGE_THRESHOLD ?= 95.0
LOAD_RACE ?= 1
ifeq ($(LOAD_RACE),0)
LOAD_RACE_FLAG :=
else
LOAD_RACE_FLAG := -race
endif

DOCKER_COMPOSE ?= docker compose -f docker-compose.yml -f docker-compose.dev.yml
DOCKER_COMPOSE_TEST ?= docker compose -f docker-compose.test.yml
DOCKER_BUILDKIT ?= 1
DOCKER_CACHE_NAMESPACE ?= ovasabi-foundation-v1
DOCKER_BUILD_ARGS ?= --build-arg CACHE_NAMESPACE=$(DOCKER_CACHE_NAMESPACE) --build-arg BUILDKIT_INLINE_CACHE=1

DB_USER ?= postgres
DB_PASSWORD ?= postgres
DB_NAME ?= $(PROJECT_NAME)_dev
DB_PORT ?= 5432
POSTGRES_PORT ?= $(DB_PORT)

TEST_DB_HOST ?= localhost
TEST_DB_PORT ?= 5433
TEST_DB_USER ?= $(DB_USER)
TEST_DB_PASSWORD ?= $(DB_PASSWORD)
TEST_DB_NAME ?= $(PROJECT_NAME)_test
TEST_DATABASE_URL ?= postgresql://$(TEST_DB_USER):$(TEST_DB_PASSWORD)@$(TEST_DB_HOST):$(TEST_DB_PORT)/$(TEST_DB_NAME)?sslmode=disable

TEST_REDIS_HOST ?= localhost
TEST_REDIS_PORT ?= 6380
TEST_REDIS_DB ?= 0
TEST_REDIS_PASSWORD ?=
ifeq ($(strip $(TEST_REDIS_PASSWORD)),)
TEST_REDIS_URL ?= redis://$(TEST_REDIS_HOST):$(TEST_REDIS_PORT)/$(TEST_REDIS_DB)
else
TEST_REDIS_URL ?= redis://:$(TEST_REDIS_PASSWORD)@$(TEST_REDIS_HOST):$(TEST_REDIS_PORT)/$(TEST_REDIS_DB)
endif

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

foundation-transport-proto:
	@if [ -x foundation/runtime-transport/scripts/generate_bindings.sh ]; then \
		foundation/runtime-transport/scripts/generate_bindings.sh; \
	elif [ -n "$(FOUNDATION_PATH)" ] && [ -x "$(FOUNDATION_PATH)/runtime-transport/scripts/generate_bindings.sh" ]; then \
		"$(FOUNDATION_PATH)/runtime-transport/scripts/generate_bindings.sh"; \
	else \
		echo "Runtime transport binding generator not found; skipping"; \
	fi

communication-contracts: proto proto-ts lifecycle-contracts foundation-transport-proto runtime-bindings
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

build-server:
	@if [ -f go.mod ]; then \
		mkdir -p bin; \
		$(GOBUILD) -o bin/server ./cmd/server; \
	else \
		echo "No Go module; skipping server build"; \
	fi

build-frontend:
	@if [ -f frontend/package.json ]; then \
		cd frontend && npm run build; \
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
		GOCACHE="$(GO_CACHE_DIR)" GOOS=js GOARCH=wasm $(GOBUILD) -ldflags="-s -w" -o "$(WASM_PUBLIC_DIR)/main.wasm" ./wasm; \
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
		GOCACHE="$(GO_CACHE_DIR)" GOOS=js GOARCH=wasm $(GOBUILD) -o "$(WASM_PUBLIC_DIR)/main.wasm" ./wasm; \
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

test-unit:
	@if [ -f go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)"; \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" $(GOTEST) -race -count=1 -timeout $(GO_TEST_TIMEOUT) $$packages; \
	elif [ -f backend/go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)"; \
		cd backend && \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No backend Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" $(GOTEST) -race -count=1 -timeout $(GO_TEST_TIMEOUT) $$packages; \
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
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" $(GOTEST) -race -count=1 -covermode=atomic -coverprofile="$$root_dir/coverage/go.raw.out" -timeout $(GO_TEST_TIMEOUT) $$packages; \
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
	if [ -z "$$CI" ]; then \
		$(MAKE) test-env-up; \
	fi; \
	$(MAKE) migrate-test; \
	mkdir -p "$(GO_CACHE_DIR)"; \
	APP_ENV=test \
	GOCACHE="$(GO_CACHE_DIR)" \
	TEST_INFRA_REQUIRED=true \
	TEST_REDIS_REQUIRED=true \
	TEST_DB_HOST="$(TEST_DB_HOST)" \
	TEST_DB_PORT="$(TEST_DB_PORT)" \
	TEST_DB_USER="$(TEST_DB_USER)" \
	TEST_DB_PASSWORD="$(TEST_DB_PASSWORD)" \
	TEST_DB_NAME="$(TEST_DB_NAME)" \
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
	DATABASE_URL="$(TEST_DATABASE_URL)" \
	TEST_REDIS_URL="$(TEST_REDIS_URL)" \
	REDIS_URL="$(TEST_REDIS_URL)" \
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
	if [ -z "$$CI" ]; then \
		$(MAKE) test-env-up; \
	fi; \
	$(MAKE) migrate-test; \
	mkdir -p "$(GO_CACHE_DIR)"; \
	APP_ENV=test \
	GOCACHE="$(GO_CACHE_DIR)" \
	RUN_LOAD_TESTS="$${RUN_LOAD_TESTS:-1}" \
	TEST_INFRA_REQUIRED=true \
	TEST_REDIS_REQUIRED=true \
	TEST_DB_HOST="$(TEST_DB_HOST)" \
	TEST_DB_PORT="$(TEST_DB_PORT)" \
	TEST_DB_USER="$(TEST_DB_USER)" \
	TEST_DB_PASSWORD="$(TEST_DB_PASSWORD)" \
	TEST_DB_NAME="$(TEST_DB_NAME)" \
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" \
	DATABASE_URL="$(TEST_DATABASE_URL)" \
	TEST_REDIS_URL="$(TEST_REDIS_URL)" \
	REDIS_URL="$(TEST_REDIS_URL)" \
	REDIS_PREFIX="test_$(PROJECT_NAME):" \
	$(GOTEST) -C "$$go_dir" $(LOAD_RACE_FLAG) -v -count=1 -timeout "$${LOAD_TEST_TIMEOUT:-10m}" "$$load_pkg"

test-bench:
	@if [ -f go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)"; \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" $(GOTEST) -run '^$$' -bench "$${BENCH_PATTERN:-.}" -benchmem -count "$${BENCH_COUNT:-3}" -timeout "$${BENCH_TIMEOUT:-20m}" $$packages; \
	elif [ -f backend/go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)"; \
		cd backend && \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No backend Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" $(GOTEST) -run '^$$' -bench "$${BENCH_PATTERN:-.}" -benchmem -count "$${BENCH_COUNT:-3}" -timeout "$${BENCH_TIMEOUT:-20m}" $$packages; \
	else \
		echo "No Go module; skipping benchmarks"; \
	fi

test-all: lint-foundation test-unit test-integration test-frontend

test-frontend:
	@if [ -f frontend/package.json ]; then \
		cd frontend && npm run test; \
	else \
		echo "No frontend package; skipping frontend tests"; \
	fi

test-env-up:
	@if [ ! -f docker-compose.test.yml ]; then \
		echo "docker-compose.test.yml not found"; \
		exit 1; \
	fi
	@TEST_DB_USER="$(TEST_DB_USER)" \
	TEST_DB_PASSWORD="$(TEST_DB_PASSWORD)" \
	TEST_DB_NAME="$(TEST_DB_NAME)" \
	TEST_DB_PORT="$(TEST_DB_PORT)" \
	POSTGRES_VERSION="$(POSTGRES_VERSION)" \
	TEST_POSTGRES_IMAGE="$(TEST_POSTGRES_IMAGE)" \
	TEST_REDIS_PORT="$(TEST_REDIS_PORT)" \
	TEST_REDIS_PASSWORD="$(TEST_REDIS_PASSWORD)" \
	$(DOCKER_COMPOSE_TEST) up -d test-postgres test-redis

test-env-down:
	@if [ -f docker-compose.test.yml ]; then \
		$(DOCKER_COMPOSE_TEST) down -v; \
	fi

test-env-logs:
	@if [ -f docker-compose.test.yml ]; then \
		$(DOCKER_COMPOSE_TEST) logs -f test-postgres test-redis; \
	fi

migrate-test:
	@echo "Running test migrations..."
	@if [ -f docker-compose.test.yml ] && [ -z "$$CI" ]; then \
		TEST_DB_USER="$(TEST_DB_USER)" \
		TEST_DB_PASSWORD="$(TEST_DB_PASSWORD)" \
		TEST_DB_NAME="$(TEST_DB_NAME)" \
		POSTGRES_VERSION="$(POSTGRES_VERSION)" \
		TEST_POSTGRES_IMAGE="$(TEST_POSTGRES_IMAGE)" \
		$(DOCKER_COMPOSE_TEST) run --rm test-migrate; \
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
		mkdir -p "$(GO_CACHE_DIR)"; \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" go list -f '{{.Dir}}' ./... | sed "s#^$$(pwd)#.#" | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		if command -v golangci-lint >/dev/null 2>&1; then \
			GOCACHE="$(GO_CACHE_DIR)" golangci-lint run --timeout 10m $$packages; \
		else \
			GOCACHE="$(GO_CACHE_DIR)" go vet $$packages; \
		fi; \
	fi

lint-rust:
	@if [ -f "$(RUST_WASM_DIR)/Cargo.toml" ]; then \
		if command -v cargo >/dev/null 2>&1; then \
			cargo fmt --manifest-path "$(RUST_WASM_DIR)/Cargo.toml" --all -- --check; \
			cargo clippy --manifest-path "$(RUST_WASM_DIR)/Cargo.toml" --all-targets --all-features -- -D warnings -D clippy::unwrap_used -D clippy::expect_used -D clippy::panic; \
		else \
			echo "cargo not found; skipping Rust lint"; \
		fi; \
	else \
		echo "No Rust workspace; skipping Rust lint"; \
	fi

lint-frontend:
	@if [ -f frontend/package.json ]; then \
		cd frontend && npm run lint; \
	else \
		echo "No frontend package; skipping frontend lint"; \
	fi

FOUNDATION_LINT_CHECKS := check-go-fix check-go-static-analysis check-coding-practices check-testing-practices check-go-concurrency-practices check-metadata-practices check-database-practices check-redis-practices check-river-practices check-migration-structure check-domain-contract-consistency check-contract-drift check-server-kit-usage check-project-scaffold
FOUNDATION_LINT_CHECK_TIMEOUT_SEC ?= 180

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
	@./scripts/checks/go_fix_check.sh .

check-go-static-analysis:
	@./scripts/checks/go_static_analysis_check.sh .

check-coding-practices:
	@./scripts/checks/coding_practices_check.sh .

check-testing-practices:
	@./scripts/checks/testing_practices_check.sh .

check-go-concurrency-practices:
	@./scripts/checks/go_concurrency_practices_check.sh .

check-metadata-practices:
	@./scripts/checks/metadata_practices_check.sh .

check-database-practices:
	@./scripts/checks/database_practices_check.sh .

check-redis-practices:
	@./scripts/checks/redis_practices_check.sh .

check-river-practices:
	@./scripts/checks/river_practices_check.sh .

check-migration-structure:
	@./scripts/checks/migration_structure_check.sh .

check-domain-contract-consistency:
	@./scripts/checks/domain_contract_consistency_check.sh .

check-contract-drift:
	@./scripts/checks/contract_drift_check.sh .

check-server-kit-usage:
	@./scripts/checks/server_kit_usage_check.sh .

check-project-scaffold:
	@./scripts/checks/project_scaffold_check.sh .

verify: lint test build
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
	@echo "  build            - Build backend and frontend"
	@echo "  build-runtime    - Generate runtime bindings, build WASM artifacts, and emit runtime manifest"
	@echo "  test             - Run unit and frontend tests"
	@echo "  test-unit        - Run Go tests without integration tag"
	@echo "  test-integration - Start scaffolded DB/Redis, migrate, and run integration tests"
	@echo "  test-load        - Run opt-in DB/Redis/queue recurring load tests"
	@echo "  test-bench       - Run Go benchmarks with benchmem"
	@echo "  test-all         - Run foundation checks, unit tests, integration tests, and frontend tests"
	@echo "  lint             - Run linters and scaffold checks"
	@echo "  verify           - Run lint, test, and build"
	@echo ""
	@echo "Foundation:"
	@echo "  foundation-update - Sync project-managed scaffold from foundation"
	@echo "  foundation-status - Print foundation metadata"
	@echo "  runtime-bindings  - Regenerate runtime-sdk buffer bindings"
	@echo "  lifecycle-contracts - Generate proto-derived VerifyCommandLifecycle tests"
	@echo "  wasm-manifest     - Emit frontend runtime/WASM artifact manifest"
	@echo "  native-dev        - Start the Tauri native shell against the frontend dev server"
	@echo "  native-build      - Build runtime, frontend, and native shell"
	@echo "  native-bench      - Run report-only native/runtime benchmark slices"
	@echo "  native-mobile-init - Initialize Tauri Android and iOS projects"
	@echo "  native-doctor     - Print native/Tauri environment diagnostics"
	@echo "  delivery-metrics  - Emit a local DORA/incident collection event"
	@echo ""
	@echo "Checks:"
	@echo "  check-coding-practices"
	@echo "  check-database-practices"
	@echo "  check-redis-practices"
	@echo "  check-river-practices"
	@echo "  check-migration-structure"
	 "  check-domain-contract-consistency"
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
