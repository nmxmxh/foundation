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
    image: postgres:\${POSTGRES_VERSION:-18}
    container_name: \${SERVICE_NAME:-$project_name}-postgres
    command: ["postgres", "-c", "config_file=/etc/postgresql/postgresql.conf"]
    expose:
      - "5432"
    volumes:
      # PostgreSQL 18+ stores data in major-version-specific subdirectories.
      # Mount the parent directory so pg_upgrade can work across versions.
      - postgres_data:/var/lib/postgresql
      - ./config/postgresql.conf:/etc/postgresql/postgresql.conf:ro
    environment:
      POSTGRES_USER: "\${DB_USER:-postgres}"
      POSTGRES_PASSWORD: "\${DB_PASSWORD:-postgres}"
      POSTGRES_DB: "\${DB_NAME:-$project_name}"
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
sync_go_work

if [[ "$patched" -eq 0 ]]; then
  printf '[PATCH] no managed scaffold drift found\n'
fi
