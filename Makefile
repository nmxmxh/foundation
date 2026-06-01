.PHONY: all generate-contracts build frontend-build delivery-metrics test test-go test-ts test-rust test-rust-sdk test-native-rust check-rust test-service-backed test-bench test-bench-go test-bench-native-rust test-bench-history lint verify docker-up docker-down migrate-up help \
	check-scaffold-manifest check-init-project check-update-project check-scaffold-smoke check-migration-seed-policy check-lifecycle-contract-generator \
	check-contract-drift check-go-fix check-go-static-analysis check-rust-static-analysis check-ts-static-analysis check-coding-practices check-testing-practices check-go-concurrency-practices \
	check-rust-runtime-practices check-logging-practices check-metadata-practices check-database-practices check-redis-practices check-river-practices check-migration-structure check-directory-ownership check-enforcement-integrity check-foundation-assets check-server-kit-module-contract check-server-kit-usage

.DEFAULT_GOAL := help

FOUNDATION_LINT_CHECKS := \
	check-scaffold-manifest \
	check-init-project \
	check-update-project \
	check-migration-seed-policy \
	check-lifecycle-contract-generator \
	check-contract-drift \
	check-go-fix \
	check-go-static-analysis \
	check-rust-static-analysis \
	check-ts-static-analysis \
	check-coding-practices \
	check-rust-runtime-practices \
	check-testing-practices \
	check-go-concurrency-practices \
	check-logging-practices \
	check-metadata-practices \
	check-database-practices \
	check-redis-practices \
	check-river-practices \
	check-migration-structure \
	check-directory-ownership \
	check-enforcement-integrity \
	check-foundation-assets \
	check-server-kit-module-contract \
	check-domain-contract-consistency \
	check-server-kit-usage

FOUNDATION_LINT_CHECK_TIMEOUT_SEC ?= 600
FOUNDATION_GO_CACHE_DIR ?= /tmp/ovasabi-foundation-go-build

all: build

generate-contracts:
	@echo "Generating shared runtime contracts..."
	@if [ -x runtime-transport/scripts/generate_bindings.sh ]; then runtime-transport/scripts/generate_bindings.sh; fi
	@if [ -x runtime-sdk/scripts/generate_system_bindings.sh ]; then runtime-sdk/scripts/generate_system_bindings.sh; fi

build: test-go test-rust frontend-build

frontend-build:
	@echo "Typechecking shared TypeScript packages..."
	@if [ -d runtime-transport/ts/node_modules ]; then npm --prefix runtime-transport/ts run typecheck; else echo "Skipping runtime-transport/ts typecheck; run npm install first"; fi
	@if [ -d runtime-sdk/ts/browser-host/node_modules ]; then npm --prefix runtime-sdk/ts/browser-host run typecheck; else echo "Skipping runtime-sdk/ts/browser-host typecheck; run npm install first"; fi
	@if [ -d runtime-native/ts/node_modules ]; then npm --prefix runtime-native/ts run typecheck; else echo "Skipping runtime-native/ts typecheck; run npm install first"; fi
	@if [ -d frontend-kit/ts/node_modules ]; then npm --prefix frontend-kit/ts run typecheck; else echo "Skipping frontend-kit/ts typecheck; run npm install first"; fi
	@if [ -d ui-minimal/ts/node_modules ]; then npm --prefix ui-minimal/ts run typecheck; else echo "Skipping ui-minimal/ts typecheck; run npm install first"; fi
	@if [ -d config-contracts/ts/node_modules ]; then npm --prefix config-contracts/ts run typecheck; else echo "Skipping config-contracts/ts typecheck; run npm install first"; fi

delivery-metrics:
	@node tooling/scripts/ci_delivery_metrics.mjs --out delivery-metrics/local-event.json

test: test-go test-ts test-rust

test-go:
	@echo "Running Go tests..."
	@mkdir -p "$(FOUNDATION_GO_CACHE_DIR)"
	@cd server-kit/go && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test ./...
	@cd runtime-transport/go && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test ./...
	@cd runtime-sdk/go && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test ./...
	@cd config-contracts/go && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test ./...

test-ts:
	@echo "Running TypeScript tests..."
	@if [ -d runtime-transport/ts/node_modules ]; then tooling/scripts/run_vitest.sh runtime-transport/ts run; else echo "Skipping runtime-transport/ts tests; run npm install first"; fi
	@if [ -d runtime-sdk/ts/browser-host/node_modules ]; then tooling/scripts/run_vitest.sh runtime-sdk/ts/browser-host run; else echo "Skipping runtime-sdk/ts/browser-host tests; run npm install first"; fi
	@if [ -d runtime-native/ts/node_modules ]; then tooling/scripts/run_vitest.sh runtime-native/ts run; else echo "Skipping runtime-native/ts tests; run npm install first"; fi

test-rust: test-rust-sdk test-native-rust

test-service-backed:
	@tests/service_backed_foundation_test.sh

test-rust-sdk:
	@echo "Running runtime-sdk Rust tests..."
	@cargo test --manifest-path runtime-sdk/rust/Cargo.toml

test-native-rust:
	@echo "Running runtime-native Rust tests..."
	@cargo test --manifest-path runtime-native/rust/Cargo.toml

check-rust:
	@scripts/check-rust.sh .

test-bench: test-bench-go test-bench-native-rust

test-bench-history:
	@tooling/scripts/benchmark_history.sh .

test-bench-go:
	@echo "Running bounded Foundation benchmarks..."
	@mkdir -p "$(FOUNDATION_GO_CACHE_DIR)"
	@cd server-kit/go && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test -run=^$$ -bench='Benchmark(MemoryStore|Manager)' -benchmem -benchtime=100000000ns -count=1 ./objectstore ./bulk

test-bench-native-rust:
	@echo "Running native GPU/runtime Rust benchmark simulation..."
	@cargo run --manifest-path runtime-native/rust/Cargo.toml --release --bin native_flow_sim

lint:
	@echo "Running foundation checks..."
	@tmp_log=$$(mktemp "$${TMPDIR:-/tmp}/foundation-lint.XXXXXX"); \
	trap 'rm -f "$$tmp_log"' EXIT; \
	runner="tooling/scripts/foundation_lint_check_runner.sh"; \
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
	echo "foundation checks passed"

check-scaffold-manifest:
	@tests/scaffold_manifest_test.sh

check-init-project:
	@tests/init_project_test.sh

check-update-project:
	@tests/update_project_test.sh

check-scaffold-smoke:
	@tests/scaffold_smoke_test.sh

check-migration-seed-policy:
	@tests/migration_seed_policy_test.sh

check-lifecycle-contract-generator:
	@tests/lifecycle_contract_generator_test.sh

check-contract-drift:
	@tooling/scripts/contract_drift_check.sh .

check-go-fix:
	@tooling/scripts/go_fix_check.sh .

check-go-static-analysis:
	@tooling/scripts/go_static_analysis_check.sh .

check-rust-static-analysis:
	@tooling/scripts/rust_static_analysis_check.sh .

check-ts-static-analysis:
	@tooling/scripts/ts_static_analysis_check.sh .

check-coding-practices:
	@tooling/scripts/coding_practices_check.sh .

check-rust-runtime-practices:
	@tooling/scripts/rust_runtime_practices_check.sh .

check-testing-practices:
	@tooling/scripts/testing_practices_check.sh .

check-go-concurrency-practices:
	@bash tooling/scripts/go_concurrency_practices_check.sh .

check-logging-practices:
	@tooling/scripts/logging_practices_check.sh .

check-metadata-practices:
	@tooling/scripts/metadata_practices_check.sh .

check-database-practices:
	@tooling/scripts/database_practices_check.sh .

check-redis-practices:
	@tooling/scripts/redis_practices_check.sh .

check-river-practices:
	@tooling/scripts/river_practices_check.sh .

check-migration-structure:
	@tooling/scripts/migration_structure_check.sh .

check-directory-ownership:
	@tooling/scripts/directory_ownership_check.sh .

check-enforcement-integrity:
	@tooling/scripts/enforcement_integrity_check.sh .

check-foundation-assets:
	@tooling/scripts/foundation_assets_check.sh .

check-server-kit-module-contract:
	@tooling/scripts/server_kit_module_contract_check.sh .

check-domain-contract-consistency:
	@tooling/scripts/domain_contract_consistency_check.sh .

check-server-kit-usage:
	@tooling/scripts/server_kit_usage_check.sh .

verify: lint test frontend-build check-scaffold-smoke

docker-up:
	@docker compose -f tests/docker-compose.service-backed.yml up -d

docker-down:
	@docker compose -f tests/docker-compose.service-backed.yml down -v

migrate-up:
	@echo "Core Foundation has no app migration target. Generated apps inherit make migrate-up."

help:
	@echo "Foundation core targets:"
	@echo "  make generate-contracts  Regenerate shared transport/runtime bindings"
	@echo "  make build               Run Go/Rust tests and TS typechecks"
	@echo "  make frontend-build      Typecheck shared TS packages"
	@echo "  make delivery-metrics    Emit a local DORA/incident collection event"
	@echo "  make test                Run Go, TS, and Rust tests"
	@echo "  make test-rust           Run runtime-sdk and runtime-native Rust tests"
	@echo "  make check-rust          Run Rust fmt, clippy, practice checks, and tests"
	@echo "  make test-bench          Run bounded local Foundation benchmarks"
	@echo "  make test-bench-native-rust  Run native GPU/runtime Rust benchmark simulation"
	@echo "  make lint                Run foundation scaffold/practice checks"
	@echo "  make verify              Run lint, tests, TS typechecks, and generated scaffold smoke"
	@echo "  make docker-up/down      Start/stop core service-backed test stack"
	@echo "  make check-database-practices  Run a single foundation check"
