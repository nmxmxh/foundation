.PHONY: all generate-contracts build frontend-build delivery-metrics test test-go test-ts lint verify docker-up docker-down migrate-up help

.DEFAULT_GOAL := help

all: build

generate-contracts:
	@echo "Generating shared runtime contracts..."
	@if [ -x runtime-transport/scripts/generate_bindings.sh ]; then runtime-transport/scripts/generate_bindings.sh; fi
	@if [ -x runtime-sdk/scripts/generate_system_bindings.sh ]; then runtime-sdk/scripts/generate_system_bindings.sh; fi

build: test-go frontend-build

frontend-build:
	@echo "Typechecking shared TypeScript packages..."
	@if [ -d runtime-transport/ts/node_modules ]; then npm --prefix runtime-transport/ts run typecheck; else echo "Skipping runtime-transport/ts typecheck; run npm install first"; fi
	@if [ -d runtime-sdk/ts/browser-host/node_modules ]; then npm --prefix runtime-sdk/ts/browser-host run typecheck; else echo "Skipping runtime-sdk/ts/browser-host typecheck; run npm install first"; fi
	@if [ -d frontend-kit/ts/node_modules ]; then npm --prefix frontend-kit/ts run typecheck; else echo "Skipping frontend-kit/ts typecheck; run npm install first"; fi
	@if [ -d ui-minimal/ts/node_modules ]; then npm --prefix ui-minimal/ts run typecheck; else echo "Skipping ui-minimal/ts typecheck; run npm install first"; fi
	@if [ -d config-contracts/ts/node_modules ]; then npm --prefix config-contracts/ts run typecheck; else echo "Skipping config-contracts/ts typecheck; run npm install first"; fi

delivery-metrics:
	@node tooling/scripts/ci_delivery_metrics.mjs --out delivery-metrics/local-event.json

test: test-go test-ts

test-go:
	@echo "Running Go tests..."
	@cd server-kit/go && go test ./...
	@cd runtime-transport/go && go test ./...
	@cd runtime-sdk/go && go test ./...
	@cd config-contracts/go && go test ./...

test-ts:
	@echo "Running TypeScript tests..."
	@if [ -d runtime-transport/ts/node_modules ]; then npm --prefix runtime-transport/ts run test; else echo "Skipping runtime-transport/ts tests; run npm install first"; fi
	@if [ -d runtime-sdk/ts/browser-host/node_modules ]; then npm --prefix runtime-sdk/ts/browser-host run test; else echo "Skipping runtime-sdk/ts/browser-host tests; run npm install first"; fi

lint:
	@echo "Running foundation checks..."
	@tests/scaffold_manifest_test.sh
	@tests/lifecycle_contract_generator_test.sh
	@tooling/scripts/contract_drift_check.sh .
	@tooling/scripts/go_fix_check.sh .
	@tooling/scripts/coding_practices_check.sh .
	@tooling/scripts/testing_practices_check.sh .
	@tooling/scripts/go_concurrency_practices_check.sh .
	@tooling/scripts/database_practices_check.sh .
	@tooling/scripts/redis_practices_check.sh .
	@tooling/scripts/river_practices_check.sh .
	@tooling/scripts/migration_structure_check.sh .

verify: lint test frontend-build

docker-up:
	@docker compose -f tests/docker-compose.service-backed.yml up -d

docker-down:
	@docker compose -f tests/docker-compose.service-backed.yml down -v

migrate-up:
	@echo "Core Foundation has no app migration target. Generated apps inherit make migrate-up."

help:
	@echo "Foundation core targets:"
	@echo "  make generate-contracts  Regenerate shared transport/runtime bindings"
	@echo "  make build               Run Go tests and TS typechecks"
	@echo "  make frontend-build      Typecheck shared TS packages"
	@echo "  make delivery-metrics    Emit a local DORA/incident collection event"
	@echo "  make test                Run Go and TS tests"
	@echo "  make lint                Run foundation scaffold/practice checks"
	@echo "  make verify              Run lint, tests, and TS typechecks"
	@echo "  make docker-up/down      Start/stop core service-backed test stack"
