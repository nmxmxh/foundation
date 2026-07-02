.PHONY: all generate-contracts build frontend-build delivery-metrics test test-go test-go-race test-ts test-rust test-rust-sdk test-native-rust test-rust-loom check-rust test-service-backed test-service-backed-load test-load-research test-bench test-bench-go test-bench-native-rust test-bench-frontend test-bench-history bench-simd lint verify docker-up docker-down migrate-up help \
	check-scaffold-manifest check-init-project check-update-project check-scaffold-smoke check-migration-seed-policy check-lifecycle-contract-generator check-frontend-prototype-generator check-frontend-commands-generator \
	check-contract-drift check-agent-contract check-practice-controls check-runtime-performance-contracts check-frontend-runtime-workbench check-formal-methods check-spec-conformance check-operational-excellence check-go-fix check-go-static-analysis check-rust-static-analysis check-ts-static-analysis check-coding-practices check-testing-practices check-go-concurrency-practices \
	check-rust-runtime-practices check-logging-practices check-metadata-practices check-dynamic-payload-practices check-database-practices check-atomic-lane-purity check-redis-practices check-river-practices check-migration-structure check-directory-ownership check-enforcement-integrity check-foundation-assets check-server-kit-module-contract check-server-kit-usage \
	check-doc-references check-ovasabi-cli check-benchmark-evidence check-server-kit-module-parity \
	check-lifecycle-manifest check-app-security-profile check-coverage-ratchet lifecycle-manifest

.DEFAULT_GOAL := help

FOUNDATION_LINT_CHECKS := \
	check-scaffold-manifest \
	check-init-project \
	check-update-project \
	check-scaffold-smoke \
	check-migration-seed-policy \
	check-lifecycle-contract-generator \
	check-frontend-prototype-generator \
	check-frontend-commands-generator \
	check-ovasabi-cli \
	check-contract-drift \
	check-doc-references \
	check-agent-contract \
	check-practice-controls \
	check-runtime-performance-contracts \
	check-frontend-runtime-workbench \
	check-formal-methods \
	check-spec-conformance \
	check-operational-excellence \
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
	check-dynamic-payload-practices \
	check-database-practices \
	check-atomic-lane-purity \
	check-redis-practices \
	check-river-practices \
	check-migration-structure \
	check-directory-ownership \
	check-enforcement-integrity \
	check-foundation-assets \
	check-server-kit-module-contract \
	check-server-kit-module-parity \
	check-domain-contract-consistency \
	check-server-kit-usage \
	check-benchmark-evidence \
	check-lifecycle-manifest \
	check-app-security-profile \
	check-coverage-ratchet

FOUNDATION_LINT_CHECK_TIMEOUT_SEC ?= 600
FOUNDATION_GO_CACHE_DIR ?= /tmp/ovasabi-foundation-go-build
FOUNDATION_GO_RACE_FLAGS ?=
FOUNDATION_VITEST_WORKERS ?= 0
FOUNDATION_CARGO_TEST_JOBS ?= 1
FOUNDATION_CARGO_CACHE_AUTO_CLEAN_FREQUENCY ?= never

all: build

generate-contracts:
	@echo "Generating shared runtime contracts..."
	@if [ -x runtime-transport/scripts/generate_bindings.sh ]; then runtime-transport/scripts/generate_bindings.sh; fi
	@if [ -x runtime-sdk/scripts/generate_system_bindings.sh ]; then runtime-sdk/scripts/generate_system_bindings.sh; fi
	@node tooling/scripts/generate_runtime_contract_manifest.mjs
	@node tooling/scripts/generate_frontend_commands.mjs

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

# test-go-race runs the Go suite under the data-race detector. The lock-free
# projection lanes (hermes atomic.Pointer publish, event bus, worker queues,
# registry, websocket fanout) depend on correct atomic discipline; -race is the
# strongest dynamic check that no reader observes a torn or unsynchronized
# write. It is slower than plain test-go, so it is a separate enforced gate
# (wired into verify) rather than part of the inner-loop test-go target.
test-go-race:
	@echo "Running Go tests under -race..."
	@mkdir -p "$(FOUNDATION_GO_CACHE_DIR)"
	@cd server-kit/go && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test -race $(FOUNDATION_GO_RACE_FLAGS) ./...
	@cd runtime-transport/go && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test -race $(FOUNDATION_GO_RACE_FLAGS) ./...
	@cd runtime-sdk/go && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test -race $(FOUNDATION_GO_RACE_FLAGS) ./...
	@cd config-contracts/go && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test -race $(FOUNDATION_GO_RACE_FLAGS) ./...

test-ts:
	@echo "Running TypeScript tests..."
	@if [ -d runtime-transport/ts/node_modules ]; then FOUNDATION_VITEST_WORKERS="$(FOUNDATION_VITEST_WORKERS)" tooling/scripts/run_vitest.sh runtime-transport/ts run; else echo "Skipping runtime-transport/ts tests; run npm install first"; fi
	@if [ -d runtime-sdk/ts/browser-host/node_modules ]; then FOUNDATION_VITEST_WORKERS="$(FOUNDATION_VITEST_WORKERS)" tooling/scripts/run_vitest.sh runtime-sdk/ts/browser-host run; else echo "Skipping runtime-sdk/ts/browser-host tests; run npm install first"; fi
	@if [ -d runtime-native/ts/node_modules ]; then FOUNDATION_VITEST_WORKERS="$(FOUNDATION_VITEST_WORKERS)" tooling/scripts/run_vitest.sh runtime-native/ts run; else echo "Skipping runtime-native/ts tests; run npm install first"; fi
	@if [ -d frontend-kit/ts/node_modules ]; then FOUNDATION_VITEST_WORKERS="$(FOUNDATION_VITEST_WORKERS)" tooling/scripts/run_vitest.sh frontend-kit/ts run; else echo "Skipping frontend-kit/ts tests; run npm install first"; fi

test-rust: test-rust-sdk test-native-rust

test-service-backed:
	@tests/service_backed_foundation_test.sh

test-service-backed-load:
	@tests/service_backed_load_research.sh

test-load-research:
	@tooling/scripts/load_research.sh

test-rust-sdk:
	@echo "Running runtime-sdk Rust tests..."
	@CARGO_CACHE_AUTO_CLEAN_FREQUENCY="$(FOUNDATION_CARGO_CACHE_AUTO_CLEAN_FREQUENCY)" cargo test --manifest-path runtime-sdk/rust/Cargo.toml --lib --bins -j "$(FOUNDATION_CARGO_TEST_JOBS)"

test-native-rust:
	@echo "Running runtime-native Rust tests..."
	@CARGO_CACHE_AUTO_CLEAN_FREQUENCY="$(FOUNDATION_CARGO_CACHE_AUTO_CLEAN_FREQUENCY)" cargo test --manifest-path runtime-native/rust/Cargo.toml --lib -j "$(FOUNDATION_CARGO_TEST_JOBS)"

# test-rust-loom runs the loom exhaustive-interleaving model tests for the
# lock-free log-ring publication protocol (ovrt-core). Loom enumerates thread
# interleavings and memory orderings to prove the Acquire/Release contract has
# no torn-read interleaving. It is the Rust analogue of the Go -race gate and is
# the same invocation enforced by RUST_RUNTIME_LOOM=1 in the runtime checks.
test-rust-loom:
	@echo "Running runtime-sdk loom interleaving model tests..."
	@CARGO_CACHE_AUTO_CLEAN_FREQUENCY="$(FOUNDATION_CARGO_CACHE_AUTO_CLEAN_FREQUENCY)" cargo test --manifest-path runtime-sdk/rust/Cargo.toml -p ovrt-core --features loom loom_verification -j "$(FOUNDATION_CARGO_TEST_JOBS)"

check-rust:
	@scripts/check-rust.sh .

test-bench: test-bench-go test-bench-native-rust test-bench-frontend

test-bench-history:
	@tooling/scripts/benchmark_history.sh .

# bench-simd is the explicit opt-in gate for the experimental Go SIMD lane.
# Ordinary builds stay portable (scalar fallback); this target compiles the
# amd64 archsimd path with GOEXPERIMENT=simd and runs its bounded benchmarks.
# On a non-amd64 host it measures the scalar fallback (the vector file is build-
# tag excluded), so compare against an AVX2 amd64 host for the SIMD delta.
bench-simd:
	@echo "Running opt-in Go SIMD columnar benchmarks (GOEXPERIMENT=simd)..."
	@mkdir -p "$(FOUNDATION_GO_CACHE_DIR)"
	@cd server-kit/go && GOEXPERIMENT=simd GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test -run='TestFloat64VectorSumMatchesScalarReference' -bench='BenchmarkColumnarFloat64Sum' -benchmem -count=3 ./hermes

test-bench-go:
	@echo "Running bounded Foundation benchmarks..."
	@mkdir -p "$(FOUNDATION_GO_CACHE_DIR)"
	@cd server-kit/go && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test -run=^$$ -bench='Benchmark(MemoryStore|Manager)' -benchmem -benchtime=100000000ns -count=1 ./objectstore ./bulk

test-bench-native-rust:
	@echo "Running native GPU/runtime Rust benchmark simulation..."
	@cargo run --manifest-path runtime-native/rust/Cargo.toml --release --bin native_flow_sim

test-bench-frontend:
	@echo "Running frontend workbench benchmarks and allocation profile..."
	@if [ -d frontend-kit/ts/node_modules ]; then \
		tooling/scripts/run_vitest.sh frontend-kit/ts bench --run src/runtimeWorkbench.bench.ts; \
		tooling/scripts/frontend_workbench_profile.sh .; \
	else \
		echo "Skipping frontend workbench benchmarks; run npm install in frontend-kit/ts first"; \
	fi

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

check-frontend-prototype-generator:
	@tests/frontend_prototype_generator_test.sh

check-frontend-commands-generator:
	@tests/frontend_commands_generator_test.sh

check-ovasabi-cli:
	@mkdir -p "$(FOUNDATION_GO_CACHE_DIR)"
	@cd cmd/ovasabi && GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" go test ./...
	@GOCACHE="$(FOUNDATION_GO_CACHE_DIR)" node cmd/ovasabi/bin/ovasabi.js --help >/dev/null

check-contract-drift:
	@tooling/scripts/contract_drift_check.sh .

check-doc-references:
	@node tooling/scripts/docs_reference_check.mjs .

check-agent-contract:
	@tooling/scripts/agent_contract_check.sh .

check-practice-controls:
	@tooling/scripts/practice_controls_check.sh .

check-runtime-performance-contracts:
	@tooling/scripts/runtime_performance_contract_check.sh .

check-frontend-runtime-workbench:
	@tooling/scripts/frontend_runtime_workbench_check.sh .

check-formal-methods:
	@tooling/scripts/formal_methods_check.sh .

check-spec-conformance:
	@tooling/scripts/spec_conformance_check.sh .

check-operational-excellence:
	@tooling/scripts/operational_excellence_check.sh .

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

check-dynamic-payload-practices:
	@tooling/scripts/dynamic_payload_practices_check.sh .

check-platform-boundary-debt:
	@tooling/scripts/platform_boundary_debt_check.sh .

check-database-practices:
	@tooling/scripts/database_practices_check.sh .

check-atomic-lane-purity:
	@tooling/scripts/atomic_lane_purity_check.sh .

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
	@bash tooling/scripts/server_kit_module_contract_check.sh .

check-domain-contract-consistency:
	@tooling/scripts/domain_contract_consistency_check.sh .

check-server-kit-usage:
	@tooling/scripts/server_kit_usage_check.sh .

check-server-kit-module-parity:
	@bash tooling/scripts/server_kit_module_parity_check.sh .

check-benchmark-evidence:
	@bash tooling/scripts/benchmark_evidence_check.sh .

check-lifecycle-manifest:
	@tooling/scripts/check_lifecycle_manifest.sh .

check-app-security-profile:
	@tooling/scripts/app_security_profile_check.sh .

check-coverage-ratchet:
	@FOUNDATION_GO_CACHE_DIR="$(FOUNDATION_GO_CACHE_DIR)" tooling/scripts/coverage_ratchet_check.sh .

lifecycle-manifest:
	@proto_root=api/protos; \
	if [ ! -d "$$proto_root" ]; then proto_root=templates/api/protos; fi; \
	node tooling/scripts/generate_lifecycle_manifest.mjs --proto-root "$$proto_root"


verify: lint test test-go-race frontend-build check-scaffold-smoke

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
	@echo "  make test-bench-frontend Run frontend workbench benchmarks and allocation profile"
	@echo "  make test-load-research  Run opt-in staged 1K->1M local load research"
	@echo "  make test-service-backed-load  Run opt-in staged service-backed load research"
	@echo "  make test-bench-native-rust  Run native GPU/runtime Rust benchmark simulation"
	@echo "  make bench-simd          Run opt-in Go SIMD columnar benchmarks (GOEXPERIMENT=simd)"
	@echo "  make lint                Run foundation scaffold/practice checks"
	@echo "  make verify              Run lint, tests, TS typechecks, and generated scaffold smoke"
	@echo "  make docker-up/down      Start/stop core service-backed test stack"
	@echo "  make check-agent-contract  Run the agent workflow/documentation contract check"
	@echo "  make check-doc-references  Validate local Markdown links and portable docs paths"
	@echo "  make check-lifecycle-manifest  Validate proto-derived lifecycle manifest and guide"
	@echo "  make check-app-security-profile  Validate the app-owned security profile contract"
	@echo "  make check-practice-controls  Validate the machine-readable practice controls matrix"
	@echo "  make check-runtime-performance-contracts  Validate low-level runtime performance evidence hooks"
	@echo "  make check-frontend-runtime-workbench  Validate frontend runtime/workbench separation"
	@echo "  make check-frontend-prototype-generator  Validate generated prototype schemas, stores, hooks, fixtures, and benchmark fixtures"
	@echo "  make check-formal-methods  Validate formal-method templates and spec coverage"
	@echo "  make check-operational-excellence  Validate DORA/SPACE/SLSA/OTel delivery evidence hooks"
	@echo "  make check-coverage-ratchet  Enforce per-package coverage floors ratcheting toward 95%"
	@echo "  make check-database-practices  Run a single foundation check"
