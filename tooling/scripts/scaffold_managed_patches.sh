#!/usr/bin/env bash

set -euo pipefail

target="${1:-}"
if [[ -z "$target" ]]; then
  echo "usage: scaffold_managed_patches.sh <project-path>" >&2
  exit 2
fi

if [[ ! -d "$target" ]]; then
  echo "project path not found: $target" >&2
  exit 2
fi

patched=0

log_patch() {
  printf '[PATCH] %s\n' "$1"
  patched=$((patched + 1))
}

replace_in_file() {
  local file="$1"
  local search="$2"
  local replace="$3"
  local label="$4"

  [[ -f "$file" ]] || return 0
  if ! grep -Fq -- "$search" "$file"; then
    return 0
  fi

  PATCH_SEARCH="$search" PATCH_REPLACE="$replace" perl -0pi -e 's/\Q$ENV{PATCH_SEARCH}\E/$ENV{PATCH_REPLACE}/g' "$file"
  log_patch "$label: ${file#$target/}"
}

replace_go_version_defaults() {
  local file="$1"
  [[ -f "$file" ]] || return 0

  PATCH_SEARCH='ARG GO_VERSION=1.25' PATCH_REPLACE='ARG GO_VERSION=1.26' replace_in_file "$file" 'ARG GO_VERSION=1.25' 'ARG GO_VERSION=1.26' "Go 1.26 scaffold default"
  PATCH_SEARCH='${GO_VERSION:-1.25}' PATCH_REPLACE='${GO_VERSION:-1.26}' replace_in_file "$file" '${GO_VERSION:-1.25}' '${GO_VERSION:-1.26}' "Go 1.26 scaffold default"
  PATCH_SEARCH='GO_VERSION=1.25' PATCH_REPLACE='GO_VERSION=1.26' replace_in_file "$file" 'GO_VERSION=1.25' 'GO_VERSION=1.26' "Go 1.26 scaffold default"
}

patch_generated_ignore_contract() {
  local file="$1"
  [[ -f "$file" ]] || return 0

  local broad_generated_ignore='# Generated files
**/generated/
'
  replace_in_file "$file" "$broad_generated_ignore" "" "remove blanket generated ignore"
}

patch_compose_targets() {
  local compose="$1"
  local dockerfile="$target/Dockerfile"
  [[ -f "$compose" && -f "$dockerfile" ]] || return 0

  local before
  before="$(mktemp)"
  cp "$compose" "$before"

  if grep -q ' AS final$' "$dockerfile"; then
    perl -0pi -e 's/target:\s+final-app/target: final/g' "$compose"
  fi

  if grep -q ' AS frontend$' "$dockerfile"; then
    perl -0pi -e 's/target:\s+final-nginx/target: frontend/g' "$compose"
  fi

  if ! cmp -s "$before" "$compose"; then
    log_patch "Docker Compose build target drift: ${compose#$target/}"
  fi
  rm -f "$before"
}

project_name_from_metadata() {
  local file="$target/.foundation"
  if [[ -f "$file" ]] && grep -q '^PROJECT_NAME=' "$file"; then
    sed -n 's/^PROJECT_NAME=//p' "$file" | head -n 1
    return 0
  fi
  basename "$target" | sed 's/_v[0-9]*$//'
}

patch_compose_database_contract() {
  local compose="$target/docker-compose.yml"
  [[ -f "$compose" ]] || return 0

  local project_name
  project_name="$(project_name_from_metadata)"

  if ! grep -Eq '^  postgres:' "$compose"; then
    local postgres_block
    postgres_block="$(cat <<EOF
  # PostgreSQL Database
  postgres:
    build:
      context: .
      dockerfile: Dockerfile.postgres
      args:
        POSTGRES_VERSION: "\${POSTGRES_VERSION:-18}"
    container_name: \${SERVICE_NAME:-$project_name}-postgres
    command: ["postgres", "-c", "config_file=/etc/postgresql/postgresql.conf", "-c", "hba_file=/etc/postgresql/pg_hba.conf"]
    expose:
      - "5432"
    volumes:
      # PostgreSQL 18+ stores data in major-version-specific subdirectories.
      # Mount the parent directory so pg_upgrade can work across versions.
      - postgres_data:/var/lib/postgresql
    environment:
      POSTGRES_USER: "\${DB_USER:-postgres}"
      POSTGRES_PASSWORD: "\${DB_PASSWORD:-postgres}"
      POSTGRES_DB: "\${DB_NAME:-$project_name}"
      POSTGRES_INITDB_ARGS: "--auth-host=scram-sha-256 --auth-local=trust"
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "\${DB_USER:-postgres}", "-d", "\${DB_NAME:-$project_name}"]
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s
    networks:
      - app-network
    restart: unless-stopped
EOF
)"
    insert_before_marker_or_append "$compose" "  # Redis Cache" "$postgres_block" "Docker Compose managed Postgres service"
  fi

  if ! grep -Fq '      DB_HOST: "${DB_HOST:-postgres}"' "$compose"; then
    local server_db_env
    server_db_env="$(cat <<EOF
      DATABASE_URL: "\${DATABASE_URL:-}"
      DB_HOST: "\${DB_HOST:-postgres}"
      DB_PORT: "\${DB_PORT:-5432}"
      DB_USER: "\${DB_USER:-postgres}"
      DB_PASSWORD: "\${DB_PASSWORD:-postgres}"
      DB_NAME: "\${DB_NAME:-$project_name}"
      DB_SSLMODE: "\${DB_SSLMODE:-disable}"
EOF
)"
    replace_in_file "$compose" '      DATABASE_URL: "${DATABASE_URL:-}"' "$server_db_env" "Docker Compose server database environment"
  fi

  replace_in_file "$compose" '      - DB_HOST=${DB_HOST:-}' '      - DB_HOST=${DB_HOST:-postgres}' "Docker Compose migrate database host default"
  replace_in_file "$compose" '      - DB_PASSWORD=${DB_PASSWORD:-}' '      - DB_PASSWORD=${DB_PASSWORD:-postgres}' "Docker Compose migrate database password default"

  local before_frontend
  before_frontend="$(mktemp)"
  cp "$compose" "$before_frontend"
  perl -0pi -e 's/(^  frontend:\n(?:(?!^  [A-Za-z0-9_-]+:).)*?    networks:\n      - app-network\n)    depends_on:\n      postgres:\n        condition: service_healthy\n    environment:/${1}    environment:/ms' "$compose"
  if ! cmp -s "$before_frontend" "$compose"; then
    log_patch "Docker Compose removes accidental frontend Postgres dependency: ${compose#$target/}"
  fi
  rm -f "$before_frontend"

  if ! awk '
    /^  migrate:/ { in_migrate = 1; next }
    /^  [A-Za-z0-9_-]+:/ && in_migrate { in_migrate = 0 }
    in_migrate && /depends_on:/ { found = 1 }
    END { exit found ? 0 : 1 }
  ' "$compose"; then
    local before_migrate
    before_migrate="$(mktemp)"
    cp "$compose" "$before_migrate"
    perl -0pi -e 's/(^  migrate:\n(?:(?!^  [A-Za-z0-9_-]+:).)*?    networks:\n      - app-network\n)    environment:/${1}    depends_on:\n      postgres:\n        condition: service_healthy\n    environment:/ms' "$compose"
    if ! cmp -s "$before_migrate" "$compose"; then
      log_patch "Docker Compose migrate waits for Postgres: ${compose#$target/}"
    fi
    rm -f "$before_migrate"
  fi

  if ! grep -Fq 'DATABASE_URL points at localhost' "$compose"; then
    local db_url_line='        db_url="$${DATABASE_URL:-}"'
    local db_url_guard='        db_url="$${DATABASE_URL:-}"
        if printf '\''%s'\'' "$$db_url" | grep -Eqi '\''@(localhost|127\.0\.0\.1|\[::1\]|::1)(:|/)'\''; then
          echo "DATABASE_URL points at localhost, which is the migrate container itself."
          echo "Unset DATABASE_URL and use DB_HOST=postgres, or set DATABASE_URL to the reachable Postgres service hostname."
          exit 1
        fi'
    replace_in_file "$compose" "$db_url_line" "$db_url_guard" "Docker Compose migrate rejects container-local DATABASE_URL"
  fi

  if ! grep -Eq '^  postgres_data:' "$compose"; then
    local before
    before="$(mktemp)"
    cp "$compose" "$before"
    if grep -q '^volumes:' "$compose"; then
      perl -0pi -e 's/^volumes:\n/volumes:\n  postgres_data:\n    driver: local\n/m' "$compose"
    else
      printf '\nvolumes:\n  postgres_data:\n    driver: local\n' >>"$compose"
    fi
    if ! cmp -s "$before" "$compose"; then
      log_patch "Docker Compose Postgres volume: ${compose#$target/}"
    fi
    rm -f "$before"
  fi
}

patch_coolify_deploy_contract() {
  local compose="$target/docker-compose.yml"
  local dev_compose="$target/docker-compose.dev.yml"
  local project_name
  project_name="$(project_name_from_metadata)"
  local pg_hba="$target/config/pg_hba.conf"

  if [[ -f "$pg_hba" ]]; then
    replace_in_file "$pg_hba" 'local   all             all                                     scram-sha-256' 'local   all             all                                     trust' "Postgres hba keeps local operator recovery auth"
    replace_in_file "$pg_hba" '# Require SCRAM for local, IPv4, and IPv6 clients so migration/app containers
# can connect without relying on PGDATA-generated localhost-only defaults.' '# Container-local socket access is trusted for operator recovery through
# `docker exec`. TCP clients, including migrations and app containers on the
# Compose network, must authenticate with SCRAM.' "Postgres hba documents local operator recovery auth"
  fi

  if [[ -f "$compose" ]]; then
    replace_in_file "$compose" '    image: postgres:${POSTGRES_VERSION:-18}' '    build:
      context: .
      dockerfile: Dockerfile.postgres
      args:
        POSTGRES_VERSION: "${POSTGRES_VERSION:-18}"' "Docker Compose bakes Postgres config"

    replace_in_file "$compose" '    command: ["postgres", "-c", "config_file=/etc/postgresql/postgresql.conf"]' '    command: ["postgres", "-c", "config_file=/etc/postgresql/postgresql.conf", "-c", "hba_file=/etc/postgresql/pg_hba.conf"]' "Docker Compose uses baked Postgres hba"

    replace_in_file "$compose" '      - ./config/postgresql.conf:/etc/postgresql/postgresql.conf:ro
' '' "Docker Compose removes Postgres config bind"

    replace_in_file "$compose" '      POSTGRES_INITDB_ARGS: "--auth-host=scram-sha-256 --auth-local=scram-sha-256"' '      POSTGRES_INITDB_ARGS: "--auth-host=scram-sha-256 --auth-local=trust"' "Docker Compose keeps local Postgres recovery auth"

    if ! grep -Fq 'POSTGRES_INITDB_ARGS: "--auth-host=scram-sha-256 --auth-local=trust"' "$compose"; then
      replace_in_file "$compose" '      POSTGRES_DB: "${DB_NAME:-'"$project_name"'}"' '      POSTGRES_DB: "${DB_NAME:-'"$project_name"'}"
      POSTGRES_INITDB_ARGS: "--auth-host=scram-sha-256 --auth-local=trust"' "Docker Compose sets Postgres SCRAM init auth"
      replace_in_file "$compose" '      POSTGRES_DB: "${DB_NAME:-{{PROJECT_NAME}}}"' '      POSTGRES_DB: "${DB_NAME:-{{PROJECT_NAME}}}"
      POSTGRES_INITDB_ARGS: "--auth-host=scram-sha-256 --auth-local=trust"' "Docker Compose sets Postgres SCRAM init auth"
    fi

    replace_in_file "$compose" '    image: ${REDIS_IMAGE:-redis:8-alpine}' '    build:
      context: .
      dockerfile: Dockerfile.redis
      args:
        REDIS_VERSION: "${REDIS_VERSION:-8-alpine}"' "Docker Compose bakes Redis config"

    replace_in_file "$compose" '      - ./config/redis.conf:/usr/local/etc/redis/redis.conf:ro
' '' "Docker Compose removes Redis config bind"

    replace_in_file "$compose" '      # SSL CA certificate (for managed databases)
      - ${SSL_CA_CERT_PATH:-./config/certs/ca.crt}:/etc/ssl/certs/ca.crt:ro
' '' "Docker Compose removes default CA bind"

    replace_in_file "$compose" '    volumes:
      - ${SSL_CA_CERT_PATH:-./config/certs/ca.crt}:/etc/ssl/certs/ca.crt:ro
' '' "Docker Compose removes migrate CA bind"

    # Insert auth-fail handling if it's missing entirely. Emit the grace-window
    # form so cold-volume boots (pg_isready flips healthy before the initdb role
    # exists) don't hard-exit on the first "password authentication failed".
    if ! grep -Fq 'password authentication failed' "$compose"; then
      local auth_fail_block
      auth_fail_block='          if printf '\''%s'\'' "$$output" | grep -qi '\''Dirty database version'\''; then
            echo "Database is dirty - manual intervention required"
            exit $$status
          fi
          if printf '\''%s'\'' "$$output" | grep -Eqi '\''password authentication failed|failed SASL auth|no pg_hba\.conf entry'\''; then
            elapsed=$$(( $$(date +%s) - start_ts ))
            if [ $$elapsed -ge $$auth_grace ]; then
              echo "Migration failed: database authentication still failing after $${auth_grace}s grace."
              echo "Check DB_USER, DB_PASSWORD, DB_NAME, DATABASE_URL, and the existing Postgres volume credentials."
              exit $$status
            fi
            echo "Auth not ready yet ($${elapsed}s/$${auth_grace}s grace) - retrying..."
          fi'
      replace_in_file "$compose" '          if printf '\''%s'\'' "$$output" | grep -qi '\''Dirty database version'\''; then
            echo "Database is dirty - manual intervention required"
            exit $$status
          fi' "$auth_fail_block" "Docker Compose migration retries auth during Postgres cold-boot grace window"
    fi

    # Migrate the older hard-exit auth block to the grace-window form.
    if grep -Fq 'database authentication is not retryable' "$compose"; then
      local old_auth_block new_auth_block
      old_auth_block='          if printf '\''%s'\'' "$$output" | grep -Eqi '\''password authentication failed|failed SASL auth|no pg_hba\.conf entry'\''; then
            echo "Migration failed: database authentication is not retryable."
            echo "Check DB_USER, DB_PASSWORD, DB_NAME, DATABASE_URL, and the existing Postgres volume credentials."
            exit $$status
          fi'
      new_auth_block='          if printf '\''%s'\'' "$$output" | grep -Eqi '\''password authentication failed|failed SASL auth|no pg_hba\.conf entry'\''; then
            elapsed=$$(( $$(date +%s) - start_ts ))
            if [ $$elapsed -ge $$auth_grace ]; then
              echo "Migration failed: database authentication still failing after $${auth_grace}s grace."
              echo "Check DB_USER, DB_PASSWORD, DB_NAME, DATABASE_URL, and the existing Postgres volume credentials."
              exit $$status
            fi
            echo "Auth not ready yet ($${elapsed}s/$${auth_grace}s grace) - retrying..."
          fi'
      replace_in_file "$compose" "$old_auth_block" "$new_auth_block" "Docker Compose migration retries auth during Postgres cold-boot grace window"
    fi

    # Introduce start_ts / auth_grace bookkeeping alongside attempt/max_attempts.
    if ! grep -Fq 'auth_grace=$${MIGRATE_AUTH_GRACE_SECONDS' "$compose"; then
      replace_in_file "$compose" '        attempt=1
        max_attempts=$${MIGRATE_MAX_RETRIES:-120}
        while' '        attempt=1
        max_attempts=$${MIGRATE_MAX_RETRIES:-120}
        auth_grace=$${MIGRATE_AUTH_GRACE_SECONDS:-30}
        start_ts=$$(date +%s)
        while' "Docker Compose migration tracks auth-grace elapsed time"
    fi

    # Publish MIGRATE_AUTH_GRACE_SECONDS in the migrate env list.
    if ! grep -Fq 'MIGRATE_AUTH_GRACE_SECONDS' "$compose"; then
      replace_in_file "$compose" '      - MIGRATE_MAX_RETRIES=${MIGRATE_MAX_RETRIES:-120}
      - MIGRATE_PATH=/migrations' '      - MIGRATE_MAX_RETRIES=${MIGRATE_MAX_RETRIES:-120}
      - MIGRATE_AUTH_GRACE_SECONDS=${MIGRATE_AUTH_GRACE_SECONDS:-30}
      - MIGRATE_PATH=/migrations' "Docker Compose exposes MIGRATE_AUTH_GRACE_SECONDS"
    fi

    # Replace the 12-char `dev-change-me` JWT fallback with a strong random-looking
    # literal so the compose default alone isn't trivially weak. Real deployments
    # still override via env; this only hardens the fallback path.
    replace_in_file "$compose" '      JWT_SECRET: "${JWT_SECRET:-dev-change-me}"' '      JWT_SECRET: "${JWT_SECRET:-Nx7Qk2vZpR8mYcJ4hLwT9sBdF3aVuGeHqMoI1jXnKrPyZ0AbCdEfSgUvWiOhLtMn}"' "Docker Compose strengthens JWT_SECRET fallback"
    replace_in_file "$compose" '      JWT_SECRET_KEY: "${JWT_SECRET_KEY:-dev-change-me}"' '      JWT_SECRET_KEY: "${JWT_SECRET_KEY:-Nx7Qk2vZpR8mYcJ4hLwT9sBdF3aVuGeHqMoI1jXnKrPyZ0AbCdEfSgUvWiOhLtMn}"' "Docker Compose strengthens JWT_SECRET_KEY fallback"
  fi

  if [[ -f "$dev_compose" ]]; then
    replace_in_file "$dev_compose" '    image: postgres:${POSTGRES_VERSION:-18}
' '' "dev Compose inherits baked Postgres image"
    replace_in_file "$dev_compose" '    command: ["postgres", "-c", "config_file=/etc/postgresql/postgresql.conf"]
' '' "dev Compose inherits baked Postgres command"
    replace_in_file "$dev_compose" '      - ./config/postgresql.conf:/etc/postgresql/postgresql.conf:ro
' '' "dev Compose removes Postgres config bind"
  fi
}

patch_coolify_routing_labels() {
  local compose="$target/docker-compose.yml"
  [[ -f "$compose" ]] || return 0

  # Coolify generates Traefik routers from the service FQDN configuration. The
  # scaffold-level labels used shell-style defaults inside Docker label keys,
  # which Docker Compose leaves literal and Traefik treats as invalid/noisy
  # router names. Keep routing ownership with Coolify for generated apps.
  if ! grep -Fq 'traefik.http.routers.${SERVICE_NAME:-' "$compose"; then
    return 0
  fi

  local before
  before="$(mktemp)"
  cp "$compose" "$before"
  perl -0pi -e 's/\n    labels:\n      # Traefik routing \(customize paths as needed\)\n      - "traefik\.enable=true"\n      - "traefik\.http\.routers\.\$\{SERVICE_NAME:-[^"]+-api\.rule=[^"]+"\n      - "traefik\.http\.routers\.\$\{SERVICE_NAME:-[^"]+-api\.priority=10"\n      - "traefik\.http\.services\.\$\{SERVICE_NAME:-[^"]+-api\.loadbalancer\.server\.port=8080"\n      - "traefik\.http\.services\.\$\{SERVICE_NAME:-[^"]+-api\.loadbalancer\.server\.scheme=http"\n/\n/g' "$compose"
  perl -0pi -e 's/\n    labels:\n      - "traefik\.enable=true"\n      - "traefik\.http\.routers\.\$\{SERVICE_NAME:-[^"]+-web\.rule=PathPrefix\(`\/`\)"\n      - "traefik\.http\.routers\.\$\{SERVICE_NAME:-[^"]+-web\.priority=1"\n      - "traefik\.http\.services\.\$\{SERVICE_NAME:-[^"]+-web\.loadbalancer\.server\.port=80"\n/\n/g' "$compose"
  if ! cmp -s "$before" "$compose"; then
    log_patch "Docker Compose removes scaffold Traefik labels for Coolify-owned routing: ${compose#$target/}"
  fi
  rm -f "$before"
}

patch_reframe_frontend_dockerfile() {
  local file="$target/frontend/Dockerfile"
  [[ -f "$file" ]] || return 0

  local search='FROM fholzer/nginx-brotli:v1.24.0
COPY --from=build /app/frontend/dist /usr/share/nginx/html
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["-g", "daemon off;"]'

  local replace='FROM alpine:3.23
RUN apk add --no-cache nginx nginx-mod-http-brotli ca-certificates && \
    mkdir -p /var/cache/nginx /var/run /var/log/nginx /var/lib/nginx
COPY --from=build /app/frontend/dist /usr/share/nginx/html
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]'

  PATCH_SEARCH="$search" PATCH_REPLACE="$replace" replace_in_file "$file" "$search" "$replace" "removed nginx brotli image"
}

patch_runtime_native_dockerfile() {
  local file="$target/Dockerfile"
  local package_json="$target/frontend/package.json"
  [[ -f "$file" && -f "$package_json" ]] || return 0
  grep -Fq '"@ovasabi/runtime-native"' "$package_json" || return 0

  if ! grep -Fq 'COPY foundation/runtime-native/ts/package.json ./foundation/runtime-native/ts/' "$file"; then
    PATCH_SEARCH='COPY foundation/ui-minimal/ts/package.json ./foundation/ui-minimal/ts/'
    PATCH_REPLACE='COPY foundation/ui-minimal/ts/package.json ./foundation/ui-minimal/ts/
COPY foundation/runtime-native/ts/package.json ./foundation/runtime-native/ts/'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "runtime-native Docker package manifest"
  fi

  if ! grep -Fq 'COPY foundation/runtime-native/ts ./foundation/runtime-native/ts' "$file"; then
    PATCH_SEARCH='COPY foundation/ui-minimal/ts ./foundation/ui-minimal/ts'
    PATCH_REPLACE='COPY foundation/ui-minimal/ts ./foundation/ui-minimal/ts
COPY foundation/runtime-native/ts ./foundation/runtime-native/ts'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "runtime-native Docker source"
  fi
}

patch_openapi_dockerfile() {
  local file="$target/Dockerfile"
  [[ -f "$file" ]] || return 0

  if ! grep -Fq 'go run ./cmd/docgen > /tmp/openapi.json' "$file"; then
    local search='    --mount=type=cache,id=${CACHE_NAMESPACE}-gobuild,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED="${CGO_ENABLED:-0}" go build \'
    local replace='    --mount=type=cache,id=${CACHE_NAMESPACE}-gobuild,target=/root/.cache/go-build,sharing=locked \
    go run ./cmd/docgen > /tmp/openapi.json && \
    CGO_ENABLED="${CGO_ENABLED:-0}" go build \'
    replace_in_file "$file" "$search" "$replace" "Docker server image generates OpenAPI spec"
  fi

  if ! grep -Fq 'COPY --from=builder /tmp/openapi.json ./openapi.json' "$file"; then
    replace_in_file "$file" 'COPY --from=builder /bin/server ./server' 'COPY --from=builder /bin/server ./server
COPY --from=builder /tmp/openapi.json ./openapi.json' "Docker server image embeds OpenAPI spec"
  fi
}

patch_apidocs_server() {
  local file="$target/internal/server/server.go"
  [[ -f "$file" ]] || return 0

  if ! grep -Fq 'server-kit/go/apidocs' "$file"; then
    replace_in_file "$file" '	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"' '	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/apidocs"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"' "server imports Foundation API docs handler"
  fi

  if ! grep -Fq 'apiDocs  *apidocs.Handler' "$file"; then
    if grep -Fq '	routes   []registry.HTTPRoute' "$file"; then
      replace_in_file "$file" '	routes   []registry.HTTPRoute' '	routes   []registry.HTTPRoute
	apiDocs  *apidocs.Handler' "server stores API docs handler"
    elif grep -Fq '	rbac     *security.Authorizer' "$file"; then
      replace_in_file "$file" '	rbac     *security.Authorizer' '	rbac     *security.Authorizer
	apiDocs  *apidocs.Handler' "server stores API docs handler"
    else
      replace_in_file "$file" '	jwt      *auth.JWTManager' '	jwt      *auth.JWTManager
	apiDocs  *apidocs.Handler' "server stores API docs handler"
    fi
  fi

  if ! grep -Fq '"/openapi.json"' "$file"; then
    local before_public_paths
    before_public_paths="$(mktemp)"
    cp "$file" "$before_public_paths"
    perl -0pi -e 's/(publicPaths:\s*\[\]string\{\n(?:(?!\n\t\t\},).)*?\n\t\t\t"\/ws",)/$1\n\t\t\t"\/openapi.json",\n\t\t\t"\/docs",/s' "$file"
    if ! cmp -s "$before_public_paths" "$file"; then
      log_patch "server marks API docs public: ${file#$target/}"
    fi
    rm -f "$before_public_paths"
  fi

  if ! grep -Fq 'apidocs.New(apidocs.Options{})' "$file"; then
    if grep -Fq '		apiRateLimitEnabled:     true,' "$file"; then
      replace_in_file "$file" '		apiRateLimitEnabled:     true,' '		apiDocs:                 apidocs.New(apidocs.Options{}),
		apiRateLimitEnabled:     true,' "server initializes API docs handler"
    else
      replace_in_file "$file" '		apiRateLimitEnabled:  true,' '		apiDocs:              apidocs.New(apidocs.Options{}),
		apiRateLimitEnabled:  true,' "server initializes API docs handler"
    fi
  fi

  if ! grep -Fq 's.apiDocs.Register(mux)' "$file"; then
    if grep -Fq '	mux := http.NewServeMux()

	// Health endpoints' "$file"; then
      local search='	mux := http.NewServeMux()

	// Health endpoints'
      local replace='	mux := http.NewServeMux()

	if s.apiDocs != nil {
		s.apiDocs.Register(mux)
		if s.apiDocs.Loaded() {
			s.log.Info("api docs registered", "spec_path", s.apiDocs.SpecPath())
		} else if err := s.apiDocs.LoadError(); err != nil {
			s.log.Warn("openapi spec not found; api docs will return 404", "error", err)
		}
	}

	// Health endpoints'
      replace_in_file "$file" "$search" "$replace" "server registers public API docs endpoints"
    else
      local search='	mux := http.NewServeMux()'
      local replace='	mux := http.NewServeMux()
	if s.apiDocs != nil {
		s.apiDocs.Register(mux)
		if s.apiDocs.Loaded() {
			s.log.Info("api docs registered", "spec_path", s.apiDocs.SpecPath())
		} else if err := s.apiDocs.LoadError(); err != nil {
			s.log.Warn("openapi spec not found; api docs will return 404", "error", err)
		}
	}'
      replace_in_file "$file" "$search" "$replace" "server registers public API docs endpoints"
    fi
  fi
}

patch_docgen_pointer_helper() {
  local file="$target/cmd/docgen/main.go"
  [[ -f "$file" ]] || return 0

  replace_in_file "$file" 'schema.MinLength = intPtr(8)' 'schema.MinLength = new(8)' "docgen password min length pointer modernization"

  if grep -Fq 'func intPtr(v int) *int' "$file"; then
    local before
    before="$(mktemp)"
    cp "$file" "$before"
    perl -0pi -e 's/\nfunc intPtr\(v int\) \*int \{\n\treturn &v\n\}\n//g' "$file"
    if ! cmp -s "$before" "$file"; then
      log_patch "docgen removes obsolete int pointer helper: ${file#$target/}"
    fi
    rm -f "$before"
  fi
}

patch_docgen_named_schema_refs() {
  local file="$target/cmd/docgen/main.go"
  [[ -f "$file" ]] || return 0

  if ! grep -Fq 'ensureNamedSchema' "$file"; then
    local request_search='		if route.RequestSchema != "" && method != "get" && method != "delete" {
			op.RequestBody = &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {
						Schema: Schema{Ref: "#/components/schemas/" + route.RequestSchema},
					},
				},
			}
		} else if route.RequestType != nil {'
    local request_replace='		if route.RequestType != nil {'
    replace_in_file "$file" "$request_search" "$request_replace" "docgen prefers typed request schemas"

    local request_tail_search='			}
		}

		successStatus := route.SuccessStatusCode'
    local request_tail_replace='			}
		} else if route.RequestSchema != "" && method != "get" && method != "delete" {
			requestSchemaName := generator.ensureNamedSchema(route.RequestSchema, "Request body for "+route.EventType)
			op.RequestBody = &RequestBody{
				Required: true,
				Content: map[string]MediaType{
					"application/json": {
						Schema: Schema{Ref: "#/components/schemas/" + requestSchemaName},
					},
				},
			}
		}

		successStatus := route.SuccessStatusCode'
    replace_in_file "$file" "$request_tail_search" "$request_tail_replace" "docgen registers string request schemas"

    local response_search='		if route.NoContentResponse {
			op.Responses[successCode] = Response{Description: successDescription}
		} else if route.ResponseSchema != "" {
			op.Responses[successCode] = Response{
				Description: successDescription,
				Content: map[string]MediaType{
					"application/json": {
						Schema: Schema{Ref: "#/components/schemas/" + route.ResponseSchema},
					},
				},
			}
		} else if route.ResponseType != nil {
			responseSchemaName := generator.generateSchema(route.ResponseType)'
    local response_replace='		if route.NoContentResponse {
			op.Responses[successCode] = Response{Description: successDescription}
		} else if route.ResponseType != nil {
			responseSchemaName := generator.generateSchema(route.ResponseType)'
    replace_in_file "$file" "$response_search" "$response_replace" "docgen prefers typed response schemas"

    local response_tail_search='			}
		} else {
			defaultSchemaRef := "#/components/schemas/StandardSuccessResponse"'
    local response_tail_replace='			}
		} else if route.ResponseSchema != "" {
			responseSchemaName := generator.ensureNamedSchema(route.ResponseSchema, "Successful response for "+route.EventType)
			op.Responses[successCode] = Response{
				Description: successDescription,
				Content: map[string]MediaType{
					"application/json": {
						Schema: Schema{Ref: "#/components/schemas/" + responseSchemaName},
					},
				},
			}
		} else {
			defaultSchemaRef := "#/components/schemas/StandardSuccessResponse"'
    replace_in_file "$file" "$response_tail_search" "$response_tail_replace" "docgen registers string response schemas"

    local schema_search='func (g *schemaGenerator) generateSchema(msg proto.Message) string {
	if msg == nil {
		return ""
	}
	return g.generateMessage(msg.ProtoReflect().Descriptor())
}
'
    local schema_replace='func (g *schemaGenerator) generateSchema(msg proto.Message) string {
	if msg == nil {
		return ""
	}
	return g.generateMessage(msg.ProtoReflect().Descriptor())
}

func (g *schemaGenerator) ensureNamedSchema(name, description string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if _, exists := g.schemas[name]; !exists {
		anySchema := Schema{}
		g.schemas[name] = Schema{
			Type:                 "object",
			Description:          strings.TrimSpace(description),
			AdditionalProperties: &anySchema,
		}
	}
	if _, exists := g.bySchemaName[name]; !exists {
		g.bySchemaName[name] = ""
	}
	return name
}
'
    replace_in_file "$file" "$schema_search" "$schema_replace" "docgen named schema fallback"
  fi
}

patch_docgen_route_catalog() {
  # Evolve the create-managed docgen command so existing projects gain the
  # `route-catalog` subcommand that emits route_catalog.json for the frontend
  # command registry generator. Idempotent: skips once the branch is present.
  local file="$target/cmd/docgen/main.go"
  [[ -f "$file" ]] || return 0
  if grep -Fq 'os.Args[1] == "route-catalog"' "$file"; then
    return 0
  fi
  # The subcommand needs httpapi and bootstrap; bail out if the file does not
  # match the known scaffold shape so we never corrupt a customized docgen.
  grep -Fq 'server-kit/go/registry"' "$file" || return 0
  grep -Fq '/internal/bootstrap"' "$file" || return 0
  # Anchor after cfg is built so the branch can reuse cfg.Routes — accessor
  # agnostic, independent of which route accessor the app's bootstrap exposes.
  grep -Fq $'\tspec := Generate(cfg)' "$file" || return 0

  local before
  before="$(mktemp)"
  cp "$file" "$before"

  if ! grep -Fq 'server-kit/go/httpapi"' "$file"; then
    PATCH_SEARCH=$'\t"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"' \
    PATCH_REPLACE=$'\t"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpapi"\n\t"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"' \
      perl -0pi -e 's/\Q$ENV{PATCH_SEARCH}\E/$ENV{PATCH_REPLACE}/' "$file"
  fi

  PATCH_SEARCH=$'\tspec := Generate(cfg)' \
  PATCH_REPLACE=$'\t// `docgen route-catalog` emits the client route catalog JSON (consumed by\n\t// tooling/scripts/generate_frontend_commands.mjs) instead of the OpenAPI\n\t// spec. It reuses cfg.Routes so it stays correct regardless of which route\n\t// accessor the app'"'"'s bootstrap exposes.\n\tif len(os.Args) > 1 && os.Args[1] == "route-catalog" {\n\t\tdata, err := httpapi.MarshalRouteCatalog(cfg.Routes)\n\t\tif err != nil {\n\t\t\tfmt.Fprintf(os.Stderr, "Error encoding route catalog: %v\\n", err)\n\t\t\tos.Exit(1)\n\t\t}\n\t\tif _, err := os.Stdout.Write(data); err != nil {\n\t\t\tfmt.Fprintf(os.Stderr, "Error writing route catalog: %v\\n", err)\n\t\t\tos.Exit(1)\n\t\t}\n\t\treturn\n\t}\n\n\tspec := Generate(cfg)' \
    perl -0pi -e 's/\Q$ENV{PATCH_SEARCH}\E/$ENV{PATCH_REPLACE}/' "$file"

  if ! cmp -s "$before" "$file"; then
    log_patch "docgen route-catalog subcommand: ${file#$target/}"
  fi
  rm -f "$before"
}

patch_native_tauri_startup_expect() {
  local file="$target/native/src-tauri/src/lib.rs"
  [[ -f "$file" ]] || return 0

  local search='    tauri::Builder::default()
        .manage(NativeState::new())
        .invoke_handler(tauri::generate_handler![
            foundation_runtime_dispatch,
            foundation_runtime_capabilities,
            foundation_secure_store_get,
            foundation_secure_store_put,
            foundation_secure_store_delete
        ])
        .run(tauri::generate_context!())
        .expect("error while running Tauri application");'

  local replace='    let result = tauri::Builder::default()
        .manage(NativeState::new())
        .invoke_handler(tauri::generate_handler![
            foundation_runtime_dispatch,
            foundation_runtime_capabilities,
            foundation_secure_store_get,
            foundation_secure_store_put,
            foundation_secure_store_delete
        ])
        .run(tauri::generate_context!());

    if let Err(error) = result {
        eprintln!("tauri runtime failed: {error}");
    }'

  PATCH_SEARCH="$search" PATCH_REPLACE="$replace" replace_in_file "$file" "$search" "$replace" "native Tauri startup avoids expect"
}

patch_go_dependency_manifests() {
  local file="$target/Dockerfile"
  [[ -f "$file" ]] || return 0

  if grep -Fq 'go mod download && touch /tmp/deps-ready' "$file"; then
    PATCH_SEARCH='go mod download && touch /tmp/deps-ready'
    PATCH_REPLACE='set -eux; \
    go env GOMOD GOWORK GOPROXY; \
    find . -path '\''*/go.mod'\'' -maxdepth 5 -print | sort; \
    touch /tmp/deps-ready'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Docker Go dependency diagnostics"
  fi

  if grep -Fq '    go mod download; \' "$file"; then
    PATCH_SEARCH='    go mod download; \
'
    PATCH_REPLACE=''
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Docker Go dependency predownload gate"
  fi

  if grep -Fq '=> ./foundation/runtime-sdk/go' "$target/go.mod" && ! grep -Fq 'COPY foundation/runtime-sdk/go/go.mod ./foundation/runtime-sdk/go/' "$file"; then
    PATCH_SEARCH='COPY foundation/config-contracts/go/go.mod ./foundation/config-contracts/go/'
    PATCH_REPLACE='COPY foundation/config-contracts/go/go.mod ./foundation/config-contracts/go/
COPY foundation/runtime-sdk/go/go.mod ./foundation/runtime-sdk/go/
COPY foundation/runtime-sdk/go/go.sum ./foundation/runtime-sdk/go/'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Docker Go dependency manifest: runtime-sdk"
  fi

  if [[ -f "$target/api/protos/go.mod" ]] && grep -Fq '=> ./api/protos' "$target/go.mod" && ! grep -Fq 'COPY api/protos/go.mod ./api/protos/' "$file"; then
    PATCH_SEARCH='COPY go.sum ./'
    PATCH_REPLACE='COPY go.sum ./
COPY api/protos/go.mod ./api/protos/'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Docker Go dependency manifest: api/protos"
  fi
}

patch_server_binary_path() {
  local file="$target/Dockerfile"
  [[ -f "$file" ]] || return 0

  PATCH_SEARCH='ARG PROJECT_NAME=server
' PATCH_REPLACE='' replace_in_file "$file" 'ARG PROJECT_NAME=server
' '' "Docker server binary fixed output"
  PATCH_SEARCH='CGO_ENABLED=${CGO_ENABLED} go build' PATCH_REPLACE='CGO_ENABLED="${CGO_ENABLED:-0}" go build' replace_in_file "$file" 'CGO_ENABLED=${CGO_ENABLED} go build' 'CGO_ENABLED="${CGO_ENABLED:-0}" go build' "Docker server CGO default"
  PATCH_SEARCH='-ldflags="-s -w -X main.Version=${VERSION}"' PATCH_REPLACE='-ldflags="-s -w -X main.Version=${VERSION:-dev}"' replace_in_file "$file" '-ldflags="-s -w -X main.Version=${VERSION}"' '-ldflags="-s -w -X main.Version=${VERSION:-dev}"' "Docker server version default"
  PATCH_SEARCH='-o /bin/${PROJECT_NAME} ./cmd/server' PATCH_REPLACE='-o /bin/server ./cmd/server' replace_in_file "$file" '-o /bin/${PROJECT_NAME} ./cmd/server' '-o /bin/server ./cmd/server' "Docker server binary fixed output"
  PATCH_SEARCH='COPY --from=builder /bin/${PROJECT_NAME} ./server' PATCH_REPLACE='COPY --from=builder /bin/server ./server' replace_in_file "$file" 'COPY --from=builder /bin/${PROJECT_NAME} ./server' 'COPY --from=builder /bin/server ./server' "Docker server binary fixed copy"
}

patch_websocket_runtime_backpressure() {
  local file="$target/internal/server/websocket.go"
  [[ -f "$file" ]] || return 0

  local field_search='	cancel   context.CancelFunc

	mu            sync.RWMutex'
  local field_replace='	cancel   context.CancelFunc
	reserved bool

	mu            sync.RWMutex'
  PATCH_SEARCH="$field_search" PATCH_REPLACE="$field_replace" replace_in_file "$file" "$field_search" "$field_replace" "WebSocket reserved slot field"

  local pre_upgrade_search='	current := int(s.ws.connectionCnt.Load())
	if current >= s.wsMaxConnections {
		domainerr.WriteHTTP(w, domainerr.Unavailable("ws_capacity_reached", "websocket capacity reached"), domainerr.ResponseOptions{
			Status: http.StatusServiceUnavailable,
		})
		return
	}'
  local pre_upgrade_replace='	if !s.reserveWSConnectionSlot() {
		domainerr.WriteHTTP(w, domainerr.Unavailable("ws_capacity_reached", "websocket capacity reached"), domainerr.ResponseOptions{
			Status: http.StatusServiceUnavailable,
		})
		return
	}'
  PATCH_SEARCH="$pre_upgrade_search" PATCH_REPLACE="$pre_upgrade_replace" replace_in_file "$file" "$pre_upgrade_search" "$pre_upgrade_replace" "WebSocket capacity reserves before upgrade"

  local upgrade_search='	conn, err := s.ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error("websocket upgrade failed", "error", err.Error())
		return
	}'
  local upgrade_replace='	conn, err := s.ws.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.releaseWSConnectionSlot()
		s.log.Error("websocket upgrade failed", "error", err.Error())
		return
	}'
  PATCH_SEARCH="$upgrade_search" PATCH_REPLACE="$upgrade_replace" replace_in_file "$file" "$upgrade_search" "$upgrade_replace" "WebSocket releases slot on failed upgrade"

  local literal
  literal='reserved:  true'
  if ! grep -Fq "$literal" "$file"; then
    local construct_search='		cancel:    cancel,
		createdAt: time.Now().UTC(),'
    local construct_replace='		cancel:    cancel,
		reserved:  true,
		createdAt: time.Now().UTC(),'
    PATCH_SEARCH="$construct_search" PATCH_REPLACE="$construct_replace" replace_in_file "$file" "$construct_search" "$construct_replace" "WebSocket connection marks reserved slot"
  fi

  local rejection_search='	if !s.registerWSConnection(ctx, wsConn) {
		cancel()
		if err := conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "websocket capacity reached"),
			time.Now().Add(5*time.Second),
		); err != nil {
			s.log.Warn("failed to send rejected websocket close frame", "connection_id", connectionID, "error", err)
		}
		if err := conn.Close(); err != nil {
			s.log.Warn("failed to close rejected websocket", "error", err)
		}
		return
	}'

  local rejection_replace='	if !s.registerWSConnection(ctx, wsConn) {
		s.releaseWSConnectionSlot()
		cancel()
		if err := conn.Close(); err != nil {
			s.log.Warn("failed to close rejected websocket", "error", err)
		}
		return
	}'

  PATCH_SEARCH="$rejection_search" PATCH_REPLACE="$rejection_replace" replace_in_file "$file" "$rejection_search" "$rejection_replace" "WebSocket avoids post-upgrade capacity close"

  if ! grep -Fq 'func (s *Server) reserveWSConnectionSlot() bool' "$file"; then
    local register_marker='func (s *Server) registerWSConnection(ctx context.Context, conn *wsConnection) bool {'
    local reserve_functions='func (s *Server) reserveWSConnectionSlot() bool {
	if s == nil || s.ws == nil {
		return false
	}
	next := s.ws.connectionCnt.Add(1)
	if int(next) > s.wsMaxConnections {
		s.ws.connectionCnt.Add(-1)
		if s.ws.metrics != nil {
			s.ws.metrics.RecordConnectionRejected()
		}
		return false
	}
	return true
}

func (s *Server) releaseWSConnectionSlot() {
	if s != nil && s.ws != nil {
		s.ws.connectionCnt.Add(-1)
	}
}

func (s *Server) registerWSConnection(ctx context.Context, conn *wsConnection) bool {'
    PATCH_SEARCH="$register_marker" PATCH_REPLACE="$reserve_functions" replace_in_file "$file" "$register_marker" "$reserve_functions" "WebSocket capacity reservation helpers"
  fi

  local register_search='	next := s.ws.connectionCnt.Add(1)
	if int(next) > s.wsMaxConnections {
		s.ws.connectionCnt.Add(-1)
		if s.ws.metrics != nil {
			s.ws.metrics.RecordConnectionRejected()
		}
		return false
	}
	s.ws.connections.Store(conn.id, conn)'
  local register_replace='	if !conn.reserved {
		next := s.ws.connectionCnt.Add(1)
		if int(next) > s.wsMaxConnections {
			s.ws.connectionCnt.Add(-1)
			if s.ws.metrics != nil {
				s.ws.metrics.RecordConnectionRejected()
			}
			return false
		}
	}
	s.ws.connections.Store(conn.id, conn)'
  PATCH_SEARCH="$register_search" PATCH_REPLACE="$register_replace" replace_in_file "$file" "$register_search" "$register_replace" "WebSocket registration honors reserved slots"

  local enqueue_search='	default:
		return errors.New("websocket outbound queue full")'

  local enqueue_replace='	default:
		if s.ws != nil && s.ws.metrics != nil {
			s.ws.metrics.RecordMessageFailed()
		}
		return errors.New("websocket outbound queue full")'

  PATCH_SEARCH="$enqueue_search" PATCH_REPLACE="$enqueue_replace" replace_in_file "$file" "$enqueue_search" "$enqueue_replace" "WebSocket backpressure metric"
}

patch_typed_server_runtime() {
  local server_file="$target/internal/server/server.go"
  if [[ -f "$server_file" ]]; then
    if ! grep -Fq 'server-kit/go/extension' "$server_file"; then
      PATCH_SEARCH='	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
'
      PATCH_REPLACE='	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
'
      replace_in_file "$server_file" "$PATCH_SEARCH" "$PATCH_REPLACE" "typed server runtime import"
    fi

    replace_in_file "$server_file" 'md := metadata.FromMap(req.Metadata)' 'md := metadata.FromObject(req.Metadata)' "typed server request metadata"
    replace_in_file "$server_file" 'req.Payload = map[string]any{}' 'req.Payload = extension.Object{}' "typed server empty payload"
    replace_in_file "$server_file" 'Metadata:        md.ToMap(),' 'Metadata:        md.ToObject(),' "typed server envelope metadata"
    replace_in_file "$server_file" 'Metadata:         md.ToMap(),' 'Metadata:         md.ToObject(),' "typed server dispatch metadata"
  fi

  local ws_file="$target/internal/server/websocket.go"
  if [[ -f "$ws_file" ]]; then
    if ! grep -Fq 'server-kit/go/extension' "$ws_file"; then
      PATCH_SEARCH='	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
'
      PATCH_REPLACE='	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
'
      replace_in_file "$ws_file" "$PATCH_SEARCH" "$PATCH_REPLACE" "typed websocket runtime import"
    fi

    local ack_payload_search='		EventType: "identity:connection_open:v1:ack",
		Payload: map[string]any{
			"connection_id": conn.id,
			"state":         "guest",
		},'
    local ack_payload_replace='		EventType: "identity:connection_open:v1:ack",
		Payload: extension.Object{
			"connection_id": extension.String(conn.id),
			"state":         extension.String("guest"),
		},'
    replace_in_file "$ws_file" "$ack_payload_search" "$ack_payload_replace" "typed websocket ack payload"

    replace_in_file "$ws_file" 'md := metadata.FromMap(env.Metadata)' 'md := metadata.FromObject(env.Metadata)' "typed websocket request metadata"
    replace_in_file "$ws_file" 'md := metadata.FromMap(envelope.Metadata)' 'md := metadata.FromObject(envelope.Metadata)' "typed websocket forwarded metadata"
    replace_in_file "$ws_file" 'Metadata:         md.ToMap(),' 'Metadata:         md.ToObject(),' "typed websocket dispatch metadata"
    replace_in_file "$ws_file" 'req.Payload = map[string]any{}' 'req.Payload = extension.Object{}' "typed websocket empty payload"

    local subscribe_pattern_search='func (s *Server) handleWSSubscribe(conn *wsConnection, env events.Envelope) {
	pattern, ok := env.Payload["pattern"].(string)
	if !ok {
		pattern = ""
	}
	pattern = strings.TrimSpace(pattern)'
    local subscribe_pattern_replace='func (s *Server) handleWSSubscribe(conn *wsConnection, env events.Envelope) {
	pattern, _ := env.Payload.GetString("pattern")
	pattern = strings.TrimSpace(pattern)'
    replace_in_file "$ws_file" "$subscribe_pattern_search" "$subscribe_pattern_replace" "typed websocket subscribe payload"

    local unsubscribe_pattern_search='func (s *Server) handleWSUnsubscribe(conn *wsConnection, env events.Envelope) {
	pattern, ok := env.Payload["pattern"].(string)
	if !ok {
		pattern = ""
	}
	pattern = strings.TrimSpace(pattern)'
    local unsubscribe_pattern_replace='func (s *Server) handleWSUnsubscribe(conn *wsConnection, env events.Envelope) {
	pattern, _ := env.Payload.GetString("pattern")
	pattern = strings.TrimSpace(pattern)'
    replace_in_file "$ws_file" "$unsubscribe_pattern_search" "$unsubscribe_pattern_replace" "typed websocket unsubscribe payload"

    replace_in_file "$ws_file" 'Payload:       map[string]any{"pattern": pattern},' 'Payload:       extension.Object{"pattern": extension.String(pattern)},' "typed websocket subscription ack payload"

    local auth_signature_search='func (s *Server) maybeUpgradeConnectionAuth(ctx context.Context, conn *wsConnection, eventType string, payload map[string]any) {'
    local auth_signature_replace='func (s *Server) maybeUpgradeConnectionAuth(ctx context.Context, conn *wsConnection, eventType string, payload extension.Object) {'
    replace_in_file "$ws_file" "$auth_signature_search" "$auth_signature_replace" "typed websocket auth payload signature"

    local auth_body_search='	userID, ok := payload["user_id"].(string)
	if !ok {
		userID = ""
	}
	orgID, ok := payload["organization_id"].(string)
	if !ok {
		orgID = ""
	}
	roleID, ok := payload["role_id"].(string)
	if !ok {
		roleID = ""
	}
	rawCaps, ok := payload["capabilities"].([]any)
	if !ok {
		rawCaps = nil
	}
	caps := make([]string, 0, len(rawCaps))
	for _, capability := range rawCaps {
		if text, ok := capability.(string); ok && strings.TrimSpace(text) != "" {
			caps = append(caps, strings.TrimSpace(text))
		}
	}'
    local auth_body_replace='	userID, _ := payload.GetString("user_id")
	orgID, _ := payload.GetString("organization_id")
	roleID, _ := payload.GetString("role_id")
	rawCaps := []extension.Value{}
	if value, ok := payload["capabilities"]; ok {
		rawCaps, _ = value.ListValue()
	}
	caps := make([]string, 0, len(rawCaps))
	for _, capability := range rawCaps {
		if text, ok := capability.StringValue(); ok && strings.TrimSpace(text) != "" {
			caps = append(caps, strings.TrimSpace(text))
		}
	}'
    replace_in_file "$ws_file" "$auth_body_search" "$auth_body_replace" "typed websocket auth payload fields"

    replace_in_file "$ws_file" 'Payload:       payload,' 'Payload:       objectFromJSONValue(payload),' "typed websocket error payload"

    if ! grep -Fq 'func objectFromJSONValue(value any) extension.Object' "$ws_file"; then
      local helper_marker='func (s *Server) ensureEventSubscription() {'
      local helper='func objectFromJSONValue(value any) extension.Object {
	raw, err := json.Marshal(value)
	if err != nil {
		return extension.Object{}
	}
	payload, err := extension.ObjectFromJSON(raw)
	if err != nil {
		return extension.Object{}
	}
	return payload
}

func (s *Server) ensureEventSubscription() {'
      replace_in_file "$ws_file" "$helper_marker" "$helper" "typed websocket JSON payload helper"
    fi

    local meta_env_search='func metadataForWSEnvelope(conn *wsConnection, env events.Envelope) map[string]any {
	md := metadata.FromMap(env.Metadata)
	enrichWSMetadata(conn, &md)
	return md.ToMap()
}'
    local meta_env_replace='func metadataForWSEnvelope(conn *wsConnection, env events.Envelope) extension.Object {
	md := metadata.FromObject(env.Metadata)
	enrichWSMetadata(conn, &md)
	return md.ToObject()
}'
    replace_in_file "$ws_file" "$meta_env_search" "$meta_env_replace" "typed websocket envelope metadata helper"
    replace_in_file "$ws_file" 'func metadataForWSEnvelope(conn *wsConnection, env events.Envelope) map[string]any {' 'func metadataForWSEnvelope(conn *wsConnection, env events.Envelope) extension.Object {' "typed websocket envelope metadata helper return"
    replace_in_file "$ws_file" 'return md.ToMap()' 'return md.ToObject()' "typed websocket metadata object return"

    local meta_conn_search='func metadataForWSConnection(conn *wsConnection) map[string]any {
	md := metadata.New()
	enrichWSMetadata(conn, &md)
	return md.ToMap()
}'
    local meta_conn_replace='func metadataForWSConnection(conn *wsConnection) extension.Object {
	md := metadata.New()
	enrichWSMetadata(conn, &md)
	return md.ToObject()
}'
    replace_in_file "$ws_file" "$meta_conn_search" "$meta_conn_replace" "typed websocket connection metadata helper"

    local response_meta_search='func buildWSDispatchResponseEnvelope(request events.Envelope, md metadata.EnvelopeMetadata, result registry.DispatchResult) events.Envelope {
	meta := md.ToMap()
	meta["status"] = http.StatusOK'
    local response_meta_replace='func buildWSDispatchResponseEnvelope(request events.Envelope, md metadata.EnvelopeMetadata, result registry.DispatchResult) events.Envelope {
	meta := md.ToObject()
	meta["status"] = extension.Int(int64(http.StatusOK))'
    replace_in_file "$ws_file" "$response_meta_search" "$response_meta_replace" "typed websocket response metadata"

    local error_builder_search='func buildWSDispatchErrorEnvelope(request events.Envelope, md metadata.EnvelopeMetadata, err error) (events.Envelope, error) {
	payload := map[string]any{}
	body := domainerr.Body(err, domainerr.ResponseOptions{
		CorrelationID: request.CorrelationID,
		EventType:     request.EventType,
	})
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return events.Envelope{}, marshalErr
	}
	if decodeErr := json.Unmarshal(raw, &payload); decodeErr != nil {
		return events.Envelope{}, decodeErr
	}
	meta := md.ToMap()
	meta["status"] = domainerr.HTTPStatus(err)'
    local error_builder_replace='func buildWSDispatchErrorEnvelope(request events.Envelope, md metadata.EnvelopeMetadata, err error) (events.Envelope, error) {
	body := domainerr.Body(err, domainerr.ResponseOptions{
		CorrelationID: request.CorrelationID,
		EventType:     request.EventType,
	})
	raw, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		return events.Envelope{}, marshalErr
	}
	payload, decodeErr := extension.ObjectFromJSON(raw)
	if decodeErr != nil {
		return events.Envelope{}, decodeErr
	}
	meta := md.ToObject()
	meta["status"] = extension.Int(int64(domainerr.HTTPStatus(err)))'
    replace_in_file "$ws_file" "$error_builder_search" "$error_builder_replace" "typed websocket error envelope"
  fi
}

patch_foundation_event_log_trigger_function() {
  local migration
  while IFS= read -r migration; do
    if grep -Fq "CREATE OR REPLACE FUNCTION update_updated_at_column" "$migration"; then
      continue
    fi
    if ! grep -Fq "update_foundation_event_log_updated_at" "$migration"; then
      continue
    fi
    if ! grep -Fq "EXECUTE FUNCTION update_updated_at_column()" "$migration"; then
      continue
    fi

    perl -0pi -e 's/-- Foundation durable event log/CREATE OR REPLACE FUNCTION update_updated_at_column()\nRETURNS TRIGGER AS \$\$\nBEGIN\n    NEW.updated_at = NOW();\n    RETURN NEW;\nEND;\n\$\$ LANGUAGE plpgsql;\n\n-- Foundation durable event log/' "$migration"
    log_patch "Foundation event log trigger function: ${migration#$target/}"
  done < <(find "$target/migrations" -maxdepth 1 -type f -name '*.up.sql' 2>/dev/null | sort)
}

patch_foundation_event_log_publish_claim_schema() {
  local migration

  while IFS= read -r migration; do
    if ! grep -Fq "CREATE TABLE IF NOT EXISTS foundation_event_log" "$migration"; then
      continue
    fi
    if grep -Fq "publish_claim_expires_at" "$migration"; then
      continue
    fi

    PATCH_SEARCH='    last_publish_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),'
    PATCH_REPLACE='    last_publish_error TEXT,
    publish_claim_token TEXT,
    publish_claimed_at TIMESTAMPTZ,
    publish_claim_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),'
    replace_in_file "$migration" "$PATCH_SEARCH" "$PATCH_REPLACE" "Foundation event log publish claim columns"

    PATCH_SEARCH='CREATE INDEX IF NOT EXISTS idx_foundation_event_log_pending
    ON foundation_event_log (id)
    WHERE published_at IS NULL;'
    PATCH_REPLACE='CREATE INDEX IF NOT EXISTS idx_foundation_event_log_pending
    ON foundation_event_log (id)
    WHERE published_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_foundation_event_log_claim
    ON foundation_event_log (publish_claim_expires_at, id)
    WHERE published_at IS NULL;'
    replace_in_file "$migration" "$PATCH_SEARCH" "$PATCH_REPLACE" "Foundation event log publish claim index"
  done < <(find "$target/migrations" -maxdepth 1 -type f -name '*.up.sql' 2>/dev/null | sort)
}

patch_test_postgres_platform() {
  local file="$target/docker-compose.test.yml"
  [[ -f "$file" ]] || return 0
  grep -Fq "TEST_POSTGRES_PLATFORM" "$file" && return 0

  if grep -Fq 'image: ${TEST_POSTGRES_IMAGE:-postgres:18-alpine}' "$file"; then
    PATCH_SEARCH='    image: ${TEST_POSTGRES_IMAGE:-postgres:18-alpine}'
    PATCH_REPLACE='    image: ${TEST_POSTGRES_IMAGE:-postgres:18-alpine}
    platform: ${TEST_POSTGRES_PLATFORM:-linux/amd64}'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "test Postgres service platform"
  fi
}

patch_test_compose_ephemeral_ports() {
  local file="$target/docker-compose.test.yml"
  [[ -f "$file" ]] || return 0

  local before
  before="$(mktemp)"
  cp "$file" "$before"

  perl -0pi -e 's/#   TEST_DB_PORT: Port to expose PostgreSQL \(default: 5433\)/#   TEST_DB_PORT: Host port to expose PostgreSQL (default: 0, Docker assigns an ephemeral port)/g' "$file"
  perl -0pi -e 's/#   TEST_REDIS_PORT: Port to expose Redis \(default: 6380\)/#   TEST_REDIS_PORT: Host port to expose Redis (default: 0, Docker assigns an ephemeral port)/g' "$file"
  perl -0pi -e 's/"\$\{TEST_DB_PORT:-5433\}:5432"/"\${TEST_DB_PORT:-0}:5432"/g' "$file"
  perl -0pi -e 's/"\$\{TEST_REDIS_PORT:-6380\}:6379"/"\${TEST_REDIS_PORT:-0}:6379"/g' "$file"
  perl -0pi -e 's/^[[:blank:]]*container_name:[^\n]*\n//mg' "$file"

  if ! cmp -s "$before" "$file"; then
    log_patch "test Compose ephemeral ports and scoped container names: ${file#$target/}"
  fi
  rm -f "$before"
}

patch_postgres_config_baseline() {
  local file="$target/config/postgresql.conf"
  [[ -f "$file" ]] || return 0

  local before
  before="$(mktemp)"
  cp "$file" "$before"

  perl -0pi -e 's/^max_wal_size\s*=.*$/max_wal_size = 4GB/m' "$file"
  perl -0pi -e 's/^min_wal_size\s*=.*$/min_wal_size = 512MB/m' "$file"
  perl -0pi -e 's/^checkpoint_timeout\s*=.*$/checkpoint_timeout = 15min/m' "$file"
  perl -0pi -e 's/^autovacuum_work_mem\s*=.*$/autovacuum_work_mem = 128MB/m' "$file"

  if ! grep -Fq 'max_wal_size = 4GB' "$file"; then
    PATCH_SEARCH='wal_level = replica'
    PATCH_REPLACE='wal_level = replica
max_wal_size = 4GB'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Postgres WAL headroom baseline"
  fi
  if ! grep -Fq 'min_wal_size = 512MB' "$file"; then
    PATCH_SEARCH='max_wal_size = 4GB'
    PATCH_REPLACE='max_wal_size = 4GB
min_wal_size = 512MB'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Postgres WAL floor baseline"
  fi
  if ! grep -Fq 'checkpoint_timeout = 15min' "$file"; then
    PATCH_SEARCH='min_wal_size = 512MB'
    PATCH_REPLACE='min_wal_size = 512MB
checkpoint_timeout = 15min'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Postgres checkpoint cadence baseline"
  fi
  if ! grep -Fq 'autovacuum_work_mem = 128MB' "$file"; then
    PATCH_SEARCH='autovacuum_max_workers = 5'
    PATCH_REPLACE='autovacuum_max_workers = 5
autovacuum_work_mem = 128MB'
    replace_in_file "$file" "$PATCH_SEARCH" "$PATCH_REPLACE" "Postgres autovacuum work memory baseline"
  fi

  if ! cmp -s "$before" "$file"; then
    log_patch "Postgres WAL/autovacuum config baseline: ${file#$target/}"
  fi
  rm -f "$before"
}

insert_before_marker_or_append() {
  local file="$1"
  local marker="$2"
  local insert="$3"
  local label="$4"

  [[ -f "$file" ]] || return 0

  local before
  before="$(mktemp)"
  cp "$file" "$before"

  if grep -Fq -- "$marker" "$file"; then
    PATCH_MARKER="$marker" PATCH_INSERT="$insert" perl -0pi -e '
      my $marker = $ENV{PATCH_MARKER};
      my $insert = $ENV{PATCH_INSERT};
      s/\n\Q$marker\E/\n$insert\n\n$marker/;
    ' "$file"
  else
    printf '\n%s\n' "$insert" >>"$file"
  fi

  if ! cmp -s "$before" "$file"; then
    log_patch "$label: ${file#$target/}"
  fi
  rm -f "$before"
}

patch_agent_native_guides() {
  local agents="$target/AGENTS.md"
  if [[ -f "$agents" ]] && ! grep -Fq "agent_operating_contract.md" "$agents"; then
    local agents_section='## Agent Operating Baseline

Before editing architecture-sensitive code, read these files in order:

1. `docs/foundation/foundation_tour.md`
2. `docs/foundation/agent_operating_contract.md`
3. `docs/foundation/foundation_architecture_contract.md`
4. `docs/foundation/practice_controls.md`
5. `docs/foundation/ai_threat_model.md` when tool, model, retrieved, generated, package, or security-sensitive input affects the change
6. The relevant practice file for the lane you are changing
7. `docs/foundation/future_practices_research.md` when proposing a new practice, security posture, performance lane, or agent workflow

Definition of Done for agent-authored changes:

1. State whether a public contract changed.
2. Identify the invariant that must still hold.
3. Leave evidence: test, benchmark, static check, review note, or migration proof.
4. Preserve or document the fallback path.
5. Name the scope boundary touched.
6. Add or update a regression guard.
7. Update docs or explain why no documentation changed.'

    insert_before_marker_or_append "$agents" "## Tech Stack" "$agents_section" "agent operating baseline"
  fi

  local claude="$target/CLAUDE.md"
  if [[ -f "$claude" ]] && ! grep -Fq "agent_operating_contract.md" "$claude"; then
    local claude_section='## Agent-Native Workflow

Before changing architecture-sensitive code, read `AGENTS.md`,
`.agents/DOMAIN_GUIDE.md`, `docs/foundation/agent_operating_contract.md`, and
`docs/foundation/practice_controls.md`. For tool, model, retrieved, generated,
package, or security-sensitive input, also read
`docs/foundation/ai_threat_model.md`.

Run `make check-agent-contract` and `make check-practice-controls` after
changing docs, scaffold, practices, or agent instructions.'

    insert_before_marker_or_append "$claude" "## Commands" "$claude_section" "Claude agent-native workflow"
  fi
  if [[ -f "$claude" ]] && ! grep -Fq "check-runtime-performance-contracts" "$claude"; then
    local claude_quality='## Runtime, Formal, and Operations Checks

Run `make check-runtime-performance-contracts`, `make check-formal-methods`, and
`make check-operational-excellence` after changing runtime lanes, formal specs,
delivery telemetry, SBOM/provenance hooks, or operations workflows.'

    insert_before_marker_or_append "$claude" "## Commands" "$claude_quality" "Claude runtime/formal/ops checks"
  fi

  local domain_guide="$target/.agents/DOMAIN_GUIDE.md"
  if [[ -f "$domain_guide" ]] && ! grep -Fq "agent_operating_contract.md" "$domain_guide"; then
    local domain_section='Before changing domain structure, read `AGENTS.md` and
`docs/foundation/agent_operating_contract.md`. Domain changes must leave
evidence for contract shape, tenant isolation, event lifecycle, and fallback
behavior.'

    insert_before_marker_or_append "$domain_guide" "You provide:" "$domain_section" "domain guide agent contract"
  fi

  local post_init="$target/.agents/POST_INIT.md"
  if [[ -f "$post_init" ]] && ! grep -Fq "agent_operating_contract.md" "$post_init"; then
    local post_section='- [ ] Read `AGENTS.md` and `docs/foundation/agent_operating_contract.md`
- [ ] Read `docs/foundation/practice_controls.md` before changing scaffold or practice rules'

    if grep -Fq -- '- [ ] Copy `.env.example` to `.env`' "$post_init"; then
      PATCH_SEARCH='- [ ] Copy `.env.example` to `.env`'
      PATCH_REPLACE="$post_section"'
- [ ] Copy `.env.example` to `.env`'
      replace_in_file "$post_init" "$PATCH_SEARCH" "$PATCH_REPLACE" "post-init agent contract"
    elif grep -Fq -- '- [x] Copy `.env.example` to `.env`' "$post_init"; then
      PATCH_SEARCH='- [x] Copy `.env.example` to `.env`'
      PATCH_REPLACE="$post_section"'
- [x] Copy `.env.example` to `.env`'
      replace_in_file "$post_init" "$PATCH_SEARCH" "$PATCH_REPLACE" "post-init agent contract"
    else
      insert_before_marker_or_append "$post_init" "## Phase 2" "$post_section" "post-init agent contract"
    fi
  fi
  if [[ -f "$post_init" ]] && ! grep -Fq "check-runtime-performance-contracts" "$post_init"; then
    local post_quality='- [ ] Run `make check-runtime-performance-contracts` for runtime, benchmark, GPU, WASM, FFI, or native-lane changes
- [ ] Run `make check-formal-methods` for queue, retry, cache, projection, socket, or fallback-ladder changes
- [ ] Run `make check-operational-excellence` for delivery metrics, incident, SBOM, provenance, or OpenTelemetry changes'

    insert_before_marker_or_append "$post_init" "- [ ] Review security checklist" "$post_quality" "post-init runtime/formal/ops checks"
  fi

  local readme="$target/README.md"
  if [[ -f "$readme" ]] && ! grep -Fq "Agent-Native Workflow" "$readme"; then
    local readme_section='## Agent-Native Workflow

Before changing architecture-sensitive code, read `AGENTS.md`,
`docs/foundation/agent_operating_contract.md`, and
`docs/foundation/practice_controls.md`. For new practices, performance lanes,
security posture, or AI-agent workflow changes, also read
`docs/foundation/future_practices_research.md`.

Agent-authored changes must leave evidence through tests, benchmarks, static
checks, review notes, or migration proof. Run `make check-agent-contract` and
`make check-practice-controls` after changing docs, scaffold, practices, or
agent instructions.'

    insert_before_marker_or_append "$readme" 'Run `make lint-foundation`' "$readme_section" "README agent-native workflow"
  fi
  if [[ -f "$readme" ]] && ! grep -Fq "check-runtime-performance-contracts" "$readme"; then
    local readme_quality='## Runtime, Formal, and Operations Checks

Use `make check-runtime-performance-contracts` for low-level runtime or
benchmark evidence hooks, `make check-formal-methods` for queue/retry/cache/
projection/socket specs, and `make check-operational-excellence` for DORA,
SPACE/DevEx, OpenTelemetry, SBOM, and provenance hooks.'

    insert_before_marker_or_append "$readme" 'Run `make lint-foundation`' "$readme_quality" "README runtime/formal/ops checks"
  fi
}

sync_go_work() {
  [[ -f "$target/go.mod" ]] || return 0

  local modules=(".")
  local optional_modules=(
    "./api/protos"
    "./foundation/config-contracts/go"
    "./foundation/runtime-sdk/go"
    "./foundation/runtime-transport/go"
    "./foundation/server-kit/go"
    "./wasm"
  )

  local module
  for module in "${optional_modules[@]}"; do
    [[ -f "$target/${module#./}/go.mod" ]] && modules+=("$module")
  done

  local tmp
  tmp="$(mktemp)"
  {
    printf 'go 1.26.0\n\n'
    printf 'use (\n'
    for module in "${modules[@]}"; do
      printf '\t%s\n' "$module"
    done
    printf ')\n'
  } >"$tmp"

  if [[ ! -f "$target/go.work" ]] || ! cmp -s "$tmp" "$target/go.work"; then
    cp "$tmp" "$target/go.work"
    log_patch "Go workspace scaffold: go.work"
  fi
  rm -f "$tmp"
}

patch_startup_dependencies_double_close_redis() {
  local file="$target/internal/startup/dependencies.go"
  [[ -f "$file" ]] || return 0

  # closeBus already closes the shared redis client. A separate
  # redisClient.Close() cleanup runs LIFO after closeBus, double-closing the
  # client and producing the "redis: client is closed" / "failed to close event
  # bus" pair on any startup that fails after event bus init.
  #
  # Match the whole `if redisClient != nil { cleanups = append(...) { redisClient.Close() ... } }`
  # block with a regex so we tolerate downstream drift (e.g. `closeErr` vs `err`
  # as the inner variable name). Only touch files that still have the double-close.
  if ! grep -Fq 'redisClient.Close()' "$file"; then
    return 0
  fi
  local before after
  before="$(mktemp)"
  after="$(mktemp)"
  cp "$file" "$before"
  perl -0pi -e 's/\n\tif redisClient != nil \{\n\t\tcleanups = append\(cleanups, func\(cleanupCtx context\.Context\) \{\n\t\t\tif \w+ := redisClient\.Close\(\); \w+ != nil \{\n\t\t\t\tkitLog\.ErrorContext\(cleanupCtx, "failed to close redis", "error", \w+\)\n\t\t\t\}\n\t\t\}\)\n\t\}\n//' "$file"
  if ! cmp -s "$before" "$file"; then
    log_patch "startup dependencies drops double-close of redis client: ${file#$target/}"
  fi
  rm -f "$before" "$after"
}

patch_frontend_tsconfig_baseurl() {
  local file="$target/frontend/tsconfig.app.json"
  [[ -f "$file" ]] || return 0

  # TypeScript 7 removed the baseUrl compiler option (TS5102) and rejects
  # non-relative path targets that relied on it (TS5090), breaking `tsc` for
  # every scaffolded frontend. Drop baseUrl and make the scaffold's path aliases
  # relative so the app keeps typechecking under tsc 7. Guarded on baseUrl so it
  # is a no-op once applied.
  grep -Fq '"baseUrl"' "$file" || return 0
  local before
  before="$(mktemp)"
  cp "$file" "$before"
  perl -0pi -e 's/[ \t]*"baseUrl":\s*"\.",\r?\n//' "$file"
  perl -0pi -e 's{"\@/\*":\s*\["src/\*"\]}{"\@/*": ["./src/*"]}' "$file"
  perl -0pi -e 's{"\@generated/\*":\s*\["src/types/protos/\*"\]}{"\@generated/*": ["./src/types/protos/*"]}' "$file"
  if ! cmp -s "$before" "$file"; then
    log_patch "frontend tsconfig drops removed baseUrl for TS7: ${file#$target/}"
  fi
  rm -f "$before"
}

patch_env_example_hermes_warm_scopes() {
  local file="$target/.env.example"
  [[ -f "$file" ]] || return 0

  # HERMES_WARM_SCOPES lets the projected store warm scopes from Postgres at
  # startup so the projection gateway serves out-of-band (e.g. SQL-seeded) rows
  # instead of "projection not found". Keep the example in sync with the new
  # server-kit capability. Idempotent: skip if the key already exists.
  grep -q '^HERMES_WARM_SCOPES=' "$file" && return 0
  grep -q '^HERMES_INDEXED_FIELDS=' "$file" || return 0
  printf '# Projection scopes to warm from Postgres at startup so the projection gateway\n# serves out-of-band (e.g. SQL-seeded) rows. Comma-separated\n# "domain:collection:organization" triples.\nHERMES_WARM_SCOPES=\n' >> "$file"
  log_patch "env example adds HERMES_WARM_SCOPES: ${file#$target/}"
}

patch_startup_projection_warming() {
  local deps="$target/internal/startup/dependencies.go"
  local cfg="$target/internal/config/config.go"
  [[ -f "$deps" && -f "$cfg" ]] || return 0

  # Wire the projected store's WarmScope (shipped in server-kit) into startup so
  # the projection gateway serves out-of-band (e.g. SQL-seeded) rows instead of
  # "projection not found". This is an *additive* injection into project-owned
  # (create-mode) files, so it is heavily guarded: bail unless the file still has
  # the exact canonical scaffold shape and identifiers we depend on. A diverged
  # app is left untouched and can adopt the wiring by hand.
  grep -Fq 'deps.Projected = projected' "$deps" || return 0
  grep -Fq $'\nfunc initDatabase(' "$deps" || return 0
  grep -Fq 'cfg *config.Config' "$deps" || return 0
  grep -Fq 'kitLog :=' "$deps" || return 0
  grep -Fq 'server-kit/go/hermes' "$deps" || return 0
  grep -Fq 'HermesIndexedFields []string' "$cfg" || return 0
  grep -Fq 'HERMES_INDEXED_FIELDS' "$cfg" || return 0

  local touched=0

  # --- config.go: struct field + env load line ---
  if ! grep -Fq 'HermesWarmScopes' "$cfg"; then
    local before; before="$(mktemp)"; cp "$cfg" "$before"
    perl -0pi -e 's/(\tHermesIndexedFields \[\]string\n)/$1\t\/\/ HermesWarmScopes lists projection scopes to eagerly warm from the database\n\t\/\/ at startup so the projection gateway serves out-of-band (e.g. SQL-seeded)\n\t\/\/ rows instead of "projection not found". Each entry is\n\t\/\/ "domain:collection:organization"; empty organization is invalid.\n\tHermesWarmScopes []string\n/' "$cfg"
    perl -0pi -e 's/(HermesIndexedFields:\s*splitCSV\(getEnv\("HERMES_INDEXED_FIELDS"[^\n]*\n)/$1\t\tHermesWarmScopes: splitCSV(getEnv("HERMES_WARM_SCOPES", "")),\n/' "$cfg"
    if ! cmp -s "$before" "$cfg"; then
      touched=1
      log_patch "config adds HermesWarmScopes: ${cfg#$target/}"
    fi
    rm -f "$before"
  fi

  # --- dependencies.go: call site + helper function ---
  if ! grep -Fq 'warmProjectionScopes' "$deps"; then
    local before; before="$(mktemp)"; cp "$deps" "$before"

    # The helper uses strings.Split/TrimSpace; not every scaffold-era file
    # imports strings.
    if ! grep -Eq $'^\t"strings"$' "$deps"; then
      perl -0pi -e 's/(\n\t"fmt"\n)/$1\t"strings"\n/' "$deps"
    fi

    # Call site: warm right after the projected store is captured.
    perl -0pi -e 's/(\n\t\tdeps\.Projected = projected\n)/$1\t\twarmProjectionScopes(ctx, projected, cfg.HermesWarmScopes, kitLog)\n/' "$deps"

    # Helper function: insert immediately before initDatabase.
    local helper; helper="$(mktemp)"
    cat > "$helper" <<'GOEOF'
// warmProjectionScopes eagerly rebuilds the hermes hot partitions for the
// configured scopes so the projection gateway serves out-of-band (e.g.
// SQL-seeded) rows instead of "projection not found". Each scope is
// "domain:collection:organization". Warming failures are logged, not fatal:
// the projected store falls back to the database on read, and a warm scope is
// re-attempted lazily on the first read-through.
func warmProjectionScopes(ctx context.Context, projected *hermes.ProjectedRuntimeStore, scopes []string, log kitlogger.Logger) {
	for _, scope := range scopes {
		parts := strings.Split(scope, ":")
		if len(parts) != 3 {
			log.WarnContext(ctx, "skipping malformed hermes warm scope; want domain:collection:organization", "scope", scope)
			continue
		}
		domain, collection, organization := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		if domain == "" || collection == "" || organization == "" {
			log.WarnContext(ctx, "skipping hermes warm scope with empty component", "scope", scope)
			continue
		}
		if err := projected.WarmScope(ctx, domain, collection, organization); err != nil {
			log.WarnContext(ctx, "failed to warm hermes projection scope; will warm lazily on first read",
				"domain", domain, "collection", collection, "organization", organization, "error", err)
			continue
		}
		log.InfoContext(ctx, "warmed hermes projection scope",
			"domain", domain, "collection", collection, "organization", organization)
	}
}

GOEOF
    HELPER="$helper" perl -0pi -e 'BEGIN{local $/; open(F,"<",$ENV{HELPER}) or die; $h=<F>; close F} s/\nfunc initDatabase\(/\n$h\nfunc initDatabase(/' "$deps"
    rm -f "$helper"

    if ! cmp -s "$before" "$deps"; then
      touched=1
      log_patch "startup wires projection warming: ${deps#$target/}"
    fi
    rm -f "$before"
  fi

  # gofmt the injected Go so the changes stay lint-clean (alignment/tabs).
  if [[ "$touched" -eq 1 ]] && command -v gofmt >/dev/null 2>&1; then
    gofmt -w "$cfg" "$deps"
  fi
}

patch_startup_envelope_fallback() {
  local deps="$target/internal/startup/dependencies.go"
  local cfg="$target/internal/config/config.go"
  local env="$target/.env.example"
  [[ -f "$deps" && -f "$cfg" ]] || return 0

  # Wire the hardened EnvelopeTailer fallback (server-kit hermes) behind
  # HERMES_ENVELOPE_FALLBACK. Runs after patch_startup_projection_warming and
  # anchors on its output plus the canonical scaffold shape; a diverged app is
  # left untouched. All injections are symbol-guarded and idempotent.
  grep -Fq 'warmProjectionScopes' "$deps" || return 0
  grep -Fq 'HermesWarmScopes' "$cfg" || return 0
  grep -Fq 'deps.HealthChecker = initHealthChecker(deps.DB, deps.Redis)' "$deps" || return 0
  grep -Fq $'\nfunc initDatabase(' "$deps" || return 0
  grep -Fq 'rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"' "$deps" || return 0
  # The injected helper passes deps.Redis as rediskit.Client; an app whose
  # Dependencies struct types Redis as a raw driver client would not compile.
  grep -Eq 'Redis[[:space:]]+rediskit\.Client' "$deps" || return 0
  grep -Fq 'getEnvBool' "$cfg" || return 0

  local touched=0

  # --- config.go: struct field + env load line ---
  if ! grep -Fq 'HermesEnvelopeFallback' "$cfg"; then
    local before; before="$(mktemp)"; cp "$cfg" "$before"
    perl -0pi -e 's/(\tHermesWarmScopes \[\]string\n)/$1\t\/\/ HermesEnvelopeFallback runs a hardened EnvelopeTailer per warm scope,\n\t\/\/ consuming canonical projection envelopes from Redis Streams\n\t\/\/ (hermes:projection:<domain>:<collection>:<organization>). It is the\n\t\/\/ fallback population path for producers that cannot share the Postgres\n\t\/\/ job queue the canonical RecordWorkerProcessor uses.\n\tHermesEnvelopeFallback bool\n/' "$cfg"
    perl -0pi -e 's/(\t*HermesWarmScopes:\s*splitCSV\(getEnv\("HERMES_WARM_SCOPES", ""\)\),\n)/$1\t\tHermesEnvelopeFallback: getEnvBool("HERMES_ENVELOPE_FALLBACK", false),\n/' "$cfg"
    if ! cmp -s "$before" "$cfg"; then
      touched=1
      log_patch "config adds HermesEnvelopeFallback: ${cfg#$target/}"
    fi
    rm -f "$before"
  fi

  # --- dependencies.go: os import + call site + helper ---
  if ! grep -Fq 'startProjectionEnvelopeFallback' "$deps"; then
    local before; before="$(mktemp)"; cp "$deps" "$before"

    if ! grep -Eq $'^\t"os"$' "$deps"; then
      perl -0pi -e 's/(\n\t"fmt"\n)/$1\t"os"\n/' "$deps"
    fi

    perl -0pi -e 's/(\n\tdeps\.HealthChecker = initHealthChecker\(deps\.DB, deps\.Redis\)\n)/\n\t\/\/ Fallback projection population: tail canonical projection envelopes from\n\t\/\/ Redis Streams for each warm scope. The canonical path is a\n\t\/\/ hermes.RecordWorkerProcessor on the River queue (durable, tx-coupled);\n\t\/\/ this tailer covers producers that cannot share the Postgres job queue.\n\tif cfg.HermesEnvelopeFallback && deps.Projected != nil \&\& deps.Redis != nil {\n\t\tstartProjectionEnvelopeFallback(ctx, deps.Projected, deps.Redis, cfg.HermesWarmScopes, kitLog)\n\t}\n$1/' "$deps"

    local helper; helper="$(mktemp)"
    cat > "$helper" <<'GOEOF'
// startProjectionEnvelopeFallback runs one hardened hermes.EnvelopeTailer per
// warm scope, consuming canonical projection envelopes
// (foundation.v1.RecordMutationBatch) from the Redis stream
// hermes:projection:<domain>:<collection>:<organization>. Poison envelopes are
// quarantined by the tailer, so only system errors (e.g. Redis down) surface
// here; each tailer restarts with a fixed backoff until ctx ends. WarmScope
// runs first so the partition is registered and seeded before deltas apply.
func startProjectionEnvelopeFallback(ctx context.Context, projected *hermes.ProjectedRuntimeStore, client rediskit.Client, scopes []string, log kitlogger.Logger) {
	consumer, err := os.Hostname()
	if err != nil || strings.TrimSpace(consumer) == "" {
		consumer = "envelope_fallback"
	}
	for _, scope := range scopes {
		parts := strings.Split(scope, ":")
		if len(parts) != 3 {
			continue // warmProjectionScopes already logged the malformed scope
		}
		domain, collection, organization := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		if domain == "" || collection == "" || organization == "" {
			continue
		}
		if err := projected.WarmScope(ctx, domain, collection, organization); err != nil {
			log.WarnContext(ctx, "envelope fallback: warm before tail failed; skipping scope",
				"domain", domain, "collection", collection, "organization", organization, "error", err)
			continue
		}
		stream := "hermes:projection:" + domain + ":" + collection + ":" + organization
		source, err := hermes.NewRedisStreamEnvelopeSource(client, stream, "hermes_projection", consumer, "")
		if err != nil {
			log.WarnContext(ctx, "envelope fallback: source init failed", "stream", stream, "error", err)
			continue
		}
		projection := projected.ProjectionName(domain, collection, organization)
		tailer, err := hermes.NewEnvelopeTailer(projected.Store(), projection, source, hermes.TailerOptions{})
		if err != nil {
			log.WarnContext(ctx, "envelope fallback: tailer init failed", "projection", projection, "error", err)
			continue
		}
		log.InfoContext(ctx, "envelope fallback: tailing projection envelopes", "stream", stream, "projection", projection)
		go func() {
			for {
				err := tailer.Run(ctx)
				if ctx.Err() != nil {
					return
				}
				log.WarnContext(ctx, "envelope fallback: tailer stopped; restarting", "stream", stream, "error", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}()
	}
}

GOEOF
    HELPER="$helper" perl -0pi -e 'BEGIN{local $/; open(F,"<",$ENV{HELPER}) or die; $h=<F>; close F} s/\nfunc initDatabase\(/\n$h\nfunc initDatabase(/' "$deps"
    rm -f "$helper"

    if ! cmp -s "$before" "$deps"; then
      touched=1
      log_patch "startup wires envelope fallback tailers: ${deps#$target/}"
    fi
    rm -f "$before"
  fi

  # --- .env.example: document the flag ---
  if [[ -f "$env" ]] && ! grep -q '^HERMES_ENVELOPE_FALLBACK=' "$env" && grep -q '^HERMES_WARM_SCOPES=' "$env"; then
    perl -0pi -e 's/(^HERMES_WARM_SCOPES=[^\n]*\n)/$1# Fallback projection population: tail canonical projection envelopes from\n# Redis Streams (hermes:projection:<domain>:<collection>:<organization>) for\n# each warm scope. Canonical path is the RecordWorkerProcessor on the River\n# queue; enable this for producers that cannot share the Postgres job queue.\nHERMES_ENVELOPE_FALLBACK=false\n/m' "$env"
    log_patch "env example adds HERMES_ENVELOPE_FALLBACK: ${env#$target/}"
  fi

  if [[ "$touched" -eq 1 ]] && command -v gofmt >/dev/null 2>&1; then
    gofmt -w "$cfg" "$deps"
  fi
}

export PATCH_SEARCH PATCH_REPLACE

patch_agent_native_guides
replace_go_version_defaults "$target/.env.example"
replace_go_version_defaults "$target/Dockerfile"
replace_go_version_defaults "$target/docker-compose.yml"
replace_go_version_defaults "$target/docker-compose.dev.yml"
replace_go_version_defaults "$target/docker-compose.test.yml"
patch_generated_ignore_contract "$target/.gitignore"
patch_generated_ignore_contract "$target/foundation/.gitignore"

patch_compose_targets "$target/docker-compose.yml"
patch_compose_targets "$target/docker-compose.dev.yml"
patch_compose_targets "$target/docker-compose.test.yml"
patch_compose_database_contract
patch_coolify_deploy_contract
patch_coolify_routing_labels
patch_reframe_frontend_dockerfile
patch_runtime_native_dockerfile
patch_openapi_dockerfile
patch_apidocs_server
patch_docgen_pointer_helper
patch_docgen_named_schema_refs
patch_docgen_route_catalog
patch_native_tauri_startup_expect
patch_go_dependency_manifests
patch_server_binary_path
patch_websocket_runtime_backpressure
patch_typed_server_runtime
patch_foundation_event_log_trigger_function
patch_foundation_event_log_publish_claim_schema
patch_test_postgres_platform
patch_test_compose_ephemeral_ports
patch_postgres_config_baseline
patch_startup_dependencies_double_close_redis
patch_frontend_tsconfig_baseurl
patch_env_example_hermes_warm_scopes
patch_startup_projection_warming
patch_startup_envelope_fallback
sync_go_work

if [[ "$patched" -eq 0 ]]; then
  printf '[PATCH] no managed scaffold drift found\n'
fi
