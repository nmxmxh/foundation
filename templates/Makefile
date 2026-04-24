.PHONY: all setup deps install-tools dev build build-server build-frontend build-wasm build-wasm-dev test test-unit test-integration test-all test-frontend test-env-up test-env-down test-env-logs migrate-test migrate-test-down migrate-test-reset lint lint-go lint-frontend lint-foundation verify clean help proto proto-ts docker-build docker-up docker-down docker-logs migrate-up migrate-down migrate-create foundation-update foundation-status check-coding-practices check-database-practices check-redis-practices check-river-practices check-migration-structure check-contract-drift check-project-scaffold

PROJECT_NAME := $(shell basename "$(CURDIR)" | sed 's/_v[0-9]*$$//')
GO_MODULE := $(shell grep -m1 "^module " go.mod 2>/dev/null | cut -d' ' -f2)
FOUNDATION_PATH := $(shell grep -m1 "^FOUNDATION_PATH=" .foundation 2>/dev/null | cut -d'=' -f2)
PROTO_PATH := api/protos

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
	@for proto_dir in $$(find $(PROTO_PATH) -mindepth 1 -maxdepth 1 -type d); do \
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
	@mkdir -p frontend/src/types/protos
	@for proto_dir in $$(find $(PROTO_PATH) -mindepth 1 -maxdepth 1 -type d); do \
		latest_version_dir=$$(ls -d $$proto_dir/v*/ 2>/dev/null | sort -V | tail -n 1); \
		if [ -d "$$latest_version_dir" ] && [ -f frontend/node_modules/.bin/protoc-gen-ts_proto ]; then \
			protoc \
				-I=$(PROTO_PATH) \
				--plugin=protoc-gen-ts_proto=./frontend/node_modules/.bin/protoc-gen-ts_proto \
				--ts_proto_out=frontend/src/types/protos \
				--ts_proto_opt=esModuleInterop=true,forceLong=string,useOptionals=messages,outputEncodeMethods=false,outputJsonMethods=false,outputClientImpl=false \
				$$(find $$latest_version_dir -name '*.proto'); \
		fi; \
	done

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

build-wasm:
	@if [ -d wasm ]; then \
		echo "Building WASM..."; \
		mkdir -p frontend/public; \
		GOOS=js GOARCH=wasm $(GOBUILD) -ldflags="-s -w" -o frontend/public/main.wasm ./wasm; \
	fi

build-wasm-dev:
	@if [ -d wasm ]; then \
		echo "Building WASM (development)..."; \
		mkdir -p frontend/public; \
		GOOS=js GOARCH=wasm $(GOBUILD) -o frontend/public/main.wasm ./wasm; \
	fi

test: test-unit test-frontend

test-unit:
	@if [ -f go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)"; \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		APP_ENV=test GOCACHE="$(GO_CACHE_DIR)" $(GOTEST) -race -count=1 -timeout $(GO_TEST_TIMEOUT) $$packages; \
	else \
		echo "No Go module; skipping Go tests"; \
	fi

test-integration:
	@set -e; \
	if [ ! -f go.mod ]; then \
		echo "No Go module; skipping integration tests"; \
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
	$(GOTEST) -race -count=1 -timeout $(GO_TEST_TIMEOUT) -tags=integration ./tests/integration/...

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

lint: lint-go lint-frontend lint-foundation

lint-go:
	@if [ -f go.mod ]; then \
		mkdir -p "$(GO_CACHE_DIR)"; \
		packages=$$(GOCACHE="$(GO_CACHE_DIR)" go list ./... | grep -Ev '$(GO_PACKAGE_EXCLUDE_RE)'); \
		if [ -z "$$packages" ]; then echo "No Go packages found"; exit 0; fi; \
		if command -v golangci-lint >/dev/null 2>&1; then \
			golangci-lint run --timeout 10m $$packages; \
		else \
			GOCACHE="$(GO_CACHE_DIR)" go vet $$packages; \
		fi; \
	fi

lint-frontend:
	@if [ -f frontend/package.json ]; then \
		cd frontend && npm run lint; \
	else \
		echo "No frontend package; skipping frontend lint"; \
	fi

lint-foundation: check-coding-practices check-database-practices check-redis-practices check-river-practices check-migration-structure check-contract-drift check-project-scaffold

check-coding-practices:
	@./scripts/checks/coding_practices_check.sh .

check-database-practices:
	@./scripts/checks/database_practices_check.sh .

check-redis-practices:
	@./scripts/checks/redis_practices_check.sh .

check-river-practices:
	@./scripts/checks/river_practices_check.sh .

check-migration-structure:
	@./scripts/checks/migration_structure_check.sh .

check-contract-drift:
	@./scripts/checks/contract_drift_check.sh .

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
	@rm -f frontend/public/main.wasm frontend/public/main.wasm.br frontend/public/wasm_exec.js
	@find $(PROTO_PATH) -name "*.pb.go" -delete 2>/dev/null || true
	@find $(PROTO_PATH) -name "*_grpc.pb.go" -delete 2>/dev/null || true

help:
	@echo "Development:"
	@echo "  setup            - Install tools and dependencies"
	@echo "  dev              - Start the development server with your own runner"
	@echo "  build            - Build backend and frontend"
	@echo "  test             - Run unit and frontend tests"
	@echo "  test-unit        - Run Go tests without integration tag"
	@echo "  test-integration - Start scaffolded DB/Redis, migrate, and run integration tests"
	@echo "  test-all         - Run foundation checks, unit tests, integration tests, and frontend tests"
	@echo "  lint             - Run linters and scaffold checks"
	@echo "  verify           - Run lint, test, and build"
	@echo ""
	@echo "Foundation:"
	@echo "  foundation-update - Sync project-managed scaffold from foundation"
	@echo "  foundation-status - Print foundation metadata"
	@echo ""
	@echo "Checks:"
	@echo "  check-coding-practices"
	@echo "  check-database-practices"
	@echo "  check-redis-practices"
	@echo "  check-river-practices"
	@echo "  check-migration-structure"
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
