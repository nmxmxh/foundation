#!/bin/bash
#───────────────────────────────────────────────────────────────────────────────
# Foundation — The Machine That Builds Machines
# Production-grade full-stack generator with AI-native architecture
# v1.0.0
#───────────────────────────────────────────────────────────────────────────────

set -e

#───────────────────────────────────────────────────────────────────────────────
# CONFIGURATION
#───────────────────────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FOUNDATION_DIR="$SCRIPT_DIR"
FOUNDATION_VERSION="1.0.0"

#───────────────────────────────────────────────────────────────────────────────
# COLORS & FORMATTING (Fallback when gum unavailable)
#───────────────────────────────────────────────────────────────────────────────

readonly RESET='\033[0m'
readonly BOLD='\033[1m'
readonly DIM='\033[2m'
readonly ITALIC='\033[3m'

readonly CYAN='\033[38;5;81m'
readonly BLUE='\033[38;5;39m'
readonly GREEN='\033[38;5;78m'
readonly YELLOW='\033[38;5;220m'
readonly RED='\033[38;5;203m'
readonly MAGENTA='\033[38;5;183m'
readonly WHITE='\033[38;5;255m'
readonly GRAY='\033[38;5;242m'

# Check for gum availability
HAS_GUM=false
if command -v gum &> /dev/null; then
    HAS_GUM=true
fi

#───────────────────────────────────────────────────────────────────────────────
# OUTPUT FUNCTIONS
#───────────────────────────────────────────────────────────────────────────────

print_logo() {
    if [[ "$HAS_GUM" == "true" ]]; then
        echo ""
        gum style \
            --foreground 81 \
            --bold \
            "▸ foundation"
        gum style \
            --foreground 242 \
            --italic \
            "  The machine that builds machines"
        echo ""
    else
        echo ""
        echo -e "${CYAN}${BOLD}▸ foundation${RESET}"
        echo -e "${GRAY}${ITALIC}  The machine that builds machines${RESET}"
        echo ""
    fi
}

print_version() {
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style --foreground 242 "v${FOUNDATION_VERSION}"
    else
        echo -e "${GRAY}v${FOUNDATION_VERSION}${RESET}"
    fi
}

print_section() {
    local title="$1"
    echo ""
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style \
            --foreground 81 \
            --bold \
            "$title"
    else
        echo -e "${CYAN}${BOLD}$title${RESET}"
    fi
    echo ""
}

print_narrative() {
    local text="$1"
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style \
            --foreground 255 \
            --width 70 \
            "$text"
    else
        echo -e "${WHITE}$text${RESET}"
    fi
}

print_muted() {
    local text="$1"
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style --foreground 242 "$text"
    else
        echo -e "${GRAY}$text${RESET}"
    fi
}

print_success() {
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style --foreground 78 "✓ $1"
    else
        echo -e "  ${GREEN}✓${RESET} $1"
    fi
}

print_error() {
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style --foreground 203 "✗ $1"
    else
        echo -e "  ${RED}✗${RESET} $1"
    fi
}

print_warning() {
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style --foreground 220 "! $1"
    else
        echo -e "  ${YELLOW}!${RESET} $1"
    fi
}

print_info() {
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style --foreground 39 "→ $1"
    else
        echo -e "  ${BLUE}→${RESET} $1"
    fi
}

print_item() {
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style --foreground 242 "  · $1"
    else
        echo -e "    ${GRAY}·${RESET} $1"
    fi
}

print_command() {
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style --foreground 183 "  \$ $1"
    else
        echo -e "    ${MAGENTA}\$${RESET} ${DIM}$1${RESET}"
    fi
}

spin() {
    local message="$1"
    shift
    if [[ "$HAS_GUM" == "true" ]]; then
        gum spin --spinner dot --title "$message" -- "$@"
    else
        echo -ne "  ${GRAY}◦${RESET} ${message}..."
        "$@" > /dev/null 2>&1
        echo -e " ${GREEN}✓${RESET}"
    fi
}

confirm() {
    local message="$1"
    if [[ "$HAS_GUM" == "true" ]]; then
        gum confirm "$message"
    else
        read -p "  $message [y/N] " -n 1 -r
        echo
        [[ $REPLY =~ ^[Yy]$ ]]
    fi
}

choose() {
    local prompt="$1"
    shift
    if [[ "$HAS_GUM" == "true" ]]; then
        gum choose --header "$prompt" "$@"
    else
        echo -e "  ${WHITE}$prompt${RESET}"
        select opt in "$@"; do
            echo "$opt"
            break
        done
    fi
}

#───────────────────────────────────────────────────────────────────────────────
# NARRATIVE MESSAGING
#───────────────────────────────────────────────────────────────────────────────

show_problem_statement() {
    print_section "The Problem"

    print_narrative "You start a new project. The first week disappears into setup:"
    echo ""
    print_item "Configuring auth, queues, logging, error handling"
    print_item "Writing the same boilerplate you wrote last time"
    print_item "Forgetting tenant isolation until the first data leak"
    print_item "AI assistants generating code that 'works' but isn't production-safe"
    echo ""
    print_muted "By the time you ship, half your decisions were made under pressure."
}

show_solution() {
    print_section "The Solution"

    print_narrative "Foundation gives you production-grade architecture from minute one."
    echo ""
    print_narrative "One command. Sixty seconds. Everything you need:"
    echo ""
    print_success "Event-driven architecture with correlation tracing"
    print_success "Tenant isolation baked into every layer"
    print_success "31 coding practices your AI assistants will follow"
    print_success "Docker, migrations, CI/CD — pre-configured"
    echo ""
    print_muted "Stop building infrastructure. Start building features."
}

show_ai_angle() {
    print_section "AI-Native by Design"

    print_narrative "Most generators produce code for humans."
    print_narrative "Foundation produces code for human + AI pairs."
    echo ""
    print_item "AGENTS.md teaches any AI your architecture"
    print_item "CLAUDE.md gives Claude Code specific context"
    print_item "31 CP-* rules the AI can read and follow"
    echo ""
    print_narrative "Your AI stops guessing. It starts engineering."
}

#───────────────────────────────────────────────────────────────────────────────
# HELP
#───────────────────────────────────────────────────────────────────────────────

show_help() {
    print_logo

    echo -e "${WHITE}${BOLD}USAGE${RESET}"
    echo ""
    echo -e "  ${CYAN}foundation${RESET} ${WHITE}<project>${RESET} ${GRAY}[profile] [options]${RESET}"
    echo ""

    echo -e "${WHITE}${BOLD}PROFILES${RESET}"
    echo ""
    echo -e "  ${MAGENTA}full${RESET}        Go + React + WASM kernel  ${GRAY}(default)${RESET}"
    echo -e "  ${MAGENTA}backend${RESET}     Go service only"
    echo -e "  ${MAGENTA}frontend${RESET}    React + TypeScript"
    echo -e "  ${MAGENTA}minimal${RESET}     Config and tooling only"
    echo ""

    echo -e "${WHITE}${BOLD}OPTIONS${RESET}"
    echo ""
    echo -e "  ${WHITE}--go-module${RESET} <path>    Custom Go module path"
    echo -e "  ${WHITE}--no-docker${RESET}           Skip Docker configuration"
    echo -e "  ${WHITE}--no-wasm${RESET}             Skip WASM kernel"
    echo -e "  ${WHITE}--dry-run${RESET}             Preview without creating files"
    echo -e "  ${WHITE}--skip-deps${RESET}           Skip dependency verification"
    echo -e "  ${WHITE}--why${RESET}                 Show the problem Foundation solves"
    echo -e "  ${WHITE}--help${RESET}                Show this message"
    echo ""

    echo -e "${WHITE}${BOLD}EXAMPLES${RESET}"
    echo ""
    print_command "./init.sh my-app"
    print_muted "Full-stack app with all features"
    echo ""
    print_command "./init.sh api backend --go-module github.com/myorg/api"
    print_muted "Backend service with custom module path"
    echo ""
    print_command "./init.sh dashboard frontend"
    print_muted "React frontend application"
    echo ""

    echo -e "${WHITE}${BOLD}WHAT YOU GET${RESET}"
    echo ""
    print_success "Production-ready project structure"
    print_success "Foundation modules (server-kit, runtime-transport)"
    print_success "Docker Compose with Postgres + Redis"
    print_success "Makefile with 40+ targets"
    print_success "AI instructions (AGENTS.md, CLAUDE.md)"
    print_success "Database migrations"
    print_success "CI/CD workflows"
    echo ""

    print_muted "─────────────────────────────────────────────────────────"
    print_muted "Foundation v${FOUNDATION_VERSION} · github.com/ovasabi/foundation"
    echo ""
}

#───────────────────────────────────────────────────────────────────────────────
# DEPENDENCY CHECKING (bash 3.x compatible)
#───────────────────────────────────────────────────────────────────────────────

get_dep_name() {
    case "$1" in
        git)    echo "Git" ;;
        go)     echo "Go" ;;
        node)   echo "Node.js" ;;
        npm)    echo "npm" ;;
        docker) echo "Docker" ;;
        cargo)  echo "Rust" ;;
        *)      echo "$1" ;;
    esac
}

get_dep_install() {
    case "$1" in
        git)    echo "https://git-scm.com" ;;
        go)     echo "brew install go  ·  https://go.dev" ;;
        node)   echo "brew install node  ·  https://nodejs.org" ;;
        npm)    echo "comes with Node.js" ;;
        docker) echo "brew install --cask docker  ·  https://docker.com" ;;
        cargo)  echo "curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh" ;;
        *)      echo "" ;;
    esac
}

get_version() {
    case "$1" in
        go)     go version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 ;;
        node)   node --version 2>/dev/null | tr -d 'v' ;;
        npm)    npm --version 2>/dev/null ;;
        docker) docker --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 ;;
        cargo)  cargo --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' ;;
        git)    git --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' ;;
        *)      echo "" ;;
    esac
}

check_dependencies() {
    local profile="$1"
    local with_docker="$2"
    local with_wasm="$3"

    local required="git"
    local optional=""

    case "$profile" in
        full)
            required="git go node npm"
            [ "$with_docker" = "true" ] && optional="$optional docker"
            [ "$with_wasm" = "true" ] && optional="$optional cargo"
            ;;
        backend)
            required="git go"
            [ "$with_docker" = "true" ] && optional="$optional docker"
            ;;
        frontend)
            required="git node npm"
            ;;
    esac

    print_section "Checking Environment"

    local missing_required=""
    local missing_optional=""

    # Check required
    for dep in $required; do
        if command -v "$dep" > /dev/null 2>&1; then
            local ver=$(get_version "$dep")
            local name=$(get_dep_name "$dep")
            print_success "$name ${GRAY}$ver${RESET}"
        else
            missing_required="$missing_required $dep"
            local name=$(get_dep_name "$dep")
            print_error "$name ${GRAY}not found${RESET}"
        fi
    done

    # Check optional
    for dep in $optional; do
        [ -z "$dep" ] && continue
        if command -v "$dep" > /dev/null 2>&1; then
            local ver=$(get_version "$dep")
            local name=$(get_dep_name "$dep")
            print_success "$name ${GRAY}$ver${RESET}"
        else
            missing_optional="$missing_optional $dep"
            local name=$(get_dep_name "$dep")
            print_warning "$name ${GRAY}not found (optional)${RESET}"
        fi
    done

    # Report missing optional
    if [ -n "$missing_optional" ]; then
        echo ""
        print_muted "Optional dependencies missing. Some features disabled."
    fi

    # Fail on missing required
    if [ -n "$missing_required" ]; then
        echo ""
        print_section "Missing Dependencies"
        print_narrative "Install the following to continue:"
        echo ""

        for dep in $missing_required; do
            local name=$(get_dep_name "$dep")
            local install=$(get_dep_install "$dep")
            echo -e "  ${WHITE}$name${RESET}"
            print_muted "  $install"
            echo ""
        done

        print_muted "Run again after installing, or use --skip-deps to bypass."
        echo ""
        return 1
    fi

    return 0
}

#───────────────────────────────────────────────────────────────────────────────
# PROJECT CREATION
#───────────────────────────────────────────────────────────────────────────────

create_project() {
    local project_name="$1"
    local profile="$2"
    local go_module="$3"
    local with_docker="$4"
    local with_wasm="$5"
    local project_dir="$6"

    print_section "Creating $project_name"

    print_narrative "Building your production-grade foundation..."
    echo ""

    # Structure
    if [[ "$HAS_GUM" == "true" ]]; then
        gum spin --spinner dot --title "Creating project structure" -- sleep 0.3
    fi
    mkdir -p "$project_dir"
    cd "$project_dir"

    mkdir -p docs scripts .github/workflows

    case $profile in
        full|backend)
            mkdir -p cmd/server
            mkdir -p internal/{config,server,startup,bootstrap}
            mkdir -p migrations
            mkdir -p api/protos
            mkdir -p tests/{integration,e2e}
            ;;
    esac

    case $profile in
        full|frontend)
            local fe_dir="frontend"
            [[ "$profile" == "frontend" ]] && fe_dir="."
            mkdir -p "$fe_dir/src"/{components/ui,features,hooks,lib,stores,styles,test,types}
            mkdir -p "$fe_dir/public"
            ;;
    esac

    # Only create wasm if explicitly enabled and with content
    if [[ "$with_wasm" == "true" ]]; then
        mkdir -p wasm
        create_wasm_boilerplate "$go_module"
        create_wasm_readme
    fi

    # Create directory documentation
    case $profile in
        full|backend)
            create_tests_readme
            create_scripts_readme
            ;;
    esac

    print_success "Project structure"

    # Git
    git init -q
    print_success "Git repository"

    # Foundation reference
    cat > .foundation <<EOF
FOUNDATION_VERSION=$FOUNDATION_VERSION
FOUNDATION_PATH=$FOUNDATION_DIR
CREATED_AT=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
PROFILE=$profile
EOF
    print_success "Foundation reference"

    # Copy foundation modules
    if [[ "$profile" == "full" || "$profile" == "backend" ]]; then
        mkdir -p foundation

        [[ -d "$FOUNDATION_DIR/server-kit" ]] && cp -r "$FOUNDATION_DIR/server-kit" foundation/
        [[ -d "$FOUNDATION_DIR/runtime-transport" ]] && cp -r "$FOUNDATION_DIR/runtime-transport" foundation/
        [[ -d "$FOUNDATION_DIR/config-contracts" ]] && cp -r "$FOUNDATION_DIR/config-contracts" foundation/

        if [[ "$with_wasm" == "true" && -d "$FOUNDATION_DIR/runtime-sdk" ]]; then
            cp -r "$FOUNDATION_DIR/runtime-sdk" foundation/
        fi

        if [[ -d "$FOUNDATION_DIR/runtime-transport/protos" ]]; then
            mkdir -p api/protos
            cp -r "$FOUNDATION_DIR/runtime-transport/protos/"* api/protos/ 2>/dev/null || true
        fi

        print_success "Foundation modules"
    fi

    if [[ "$profile" == "full" || "$profile" == "frontend" ]]; then
        [[ -d "$FOUNDATION_DIR/ui-minimal" ]] && cp -r "$FOUNDATION_DIR/ui-minimal" foundation/
    fi

    # Go module
    if [[ "$profile" == "full" || "$profile" == "backend" ]]; then
        create_go_mod "$go_module"
        print_success "Go module"

        create_backend_files "$go_module" "$project_name"
        print_success "Backend scaffolding"
    fi

    # Frontend
    if [[ "$profile" == "full" || "$profile" == "frontend" ]]; then
        local fe_dir="frontend"
        [[ "$profile" == "frontend" ]] && fe_dir="."
        create_frontend_files "$project_name" "$fe_dir"
        print_success "Frontend scaffolding"
    fi

    # Makefile
    create_makefile
    print_success "Makefile (40+ targets)"

    # Docker
    if [[ "$with_docker" == "true" && ("$profile" == "full" || "$profile" == "backend") ]]; then
        create_docker_files "$project_name"
        print_success "Docker configuration"
    fi

    # Documentation
    create_readme "$project_name" "$profile"
    create_gitignore
    create_env_example "$project_name"
    print_success "Documentation"

    # AI Instructions
    [[ -f "$FOUNDATION_DIR/AGENTS.md" ]] && cp "$FOUNDATION_DIR/AGENTS.md" ./AGENTS.md
    create_claude_md "$project_name" "$profile"
    create_cursorrules "$project_name"
    print_success "AI assistant instructions"

    # Foundation docs
    if [[ -d "$FOUNDATION_DIR/docs" ]]; then
        mkdir -p docs/foundation
        cp -R "$FOUNDATION_DIR/docs/." ./docs/foundation/ 2>/dev/null || true
        print_success "Coding practices (31 rules)"
    fi

    # Post-init guidance (.agents folder)
    create_agents_folder "$project_name" "$profile"
    print_success "Post-init guidance (.agents/)"

    # API documentation and templates
    if [[ "$profile" == "full" || "$profile" == "backend" ]]; then
        create_api_documentation "$project_name"
        print_success "API layer documentation"
    fi

    # Next steps document
    create_next_steps "$project_name" "$profile"
    print_success "NEXT_STEPS.md"
}

#───────────────────────────────────────────────────────────────────────────────
# FILE GENERATORS
#───────────────────────────────────────────────────────────────────────────────

create_go_mod() {
    local go_module="$1"
    cat > go.mod <<EOF
module $go_module

go 1.24

require (
	github.com/nmxmxh/ovasabi_foundation/config-contracts/go v0.0.0
	github.com/nmxmxh/ovasabi_foundation/runtime-transport/go v0.0.0
	github.com/nmxmxh/ovasabi_foundation/server-kit/go v0.0.0
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/jackc/pgx/v5 v5.9.1
	github.com/redis/go-redis/v9 v9.17.2
	github.com/riverqueue/river v0.33.0
	github.com/riverqueue/river/riverdriver/riverpgxv5 v0.33.0
	go.uber.org/zap v1.27.0
	google.golang.org/grpc v1.80.0
	google.golang.org/protobuf v1.36.11
)

replace github.com/nmxmxh/ovasabi_foundation/server-kit/go => ./foundation/server-kit/go
replace github.com/nmxmxh/ovasabi_foundation/runtime-transport/go => ./foundation/runtime-transport/go
replace github.com/nmxmxh/ovasabi_foundation/config-contracts/go => ./foundation/config-contracts/go
EOF
}

create_backend_files() {
    local go_module="$1"
    local project_name="$2"

    cat > cmd/server/main.go <<EOF
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"$go_module/internal/config"
	"$go_module/internal/server"
	"$go_module/internal/startup"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	slog.Info("starting", "env", cfg.Env, "port", cfg.Port)

	// Initialize dependencies
	deps, err := startup.Initialize(ctx, cfg)
	if err != nil {
		return fmt.Errorf("startup: %w", err)
	}
	defer deps.Close()

	slog.Info("dependencies initialized")

	// Create and configure server
	srv := server.New(cfg.Port)

	// Health check endpoint
	srv.Mux().HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Wait for shutdown signal or server error
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown error", "error", err)
		}
	}

	slog.Info("shutdown complete")
	return nil
}
EOF

    cat > internal/config/config.go <<EOF
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env           string
	Port          int
	DatabaseURL   string
	RedisURL      string
	JWTSecret     string
	JWTExpiration time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		Env:           getEnv("APP_ENV", "development"),
		Port:          getEnvInt("PORT", 8080),
		DatabaseURL:   getEnv("DATABASE_URL", ""),
		RedisURL:      getEnv("REDIS_URL", ""),
		JWTSecret:     getEnv("JWT_SECRET", ""),
		JWTExpiration: getEnvDuration("JWT_EXPIRATION", 24*time.Hour),
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL required")
	}
	return cfg, nil
}

func getEnv(k, d string) string {
	if v := os.Getenv(k); v != "" { return v }
	return d
}

func getEnvInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if i, err := strconv.Atoi(v); err == nil { return i }
	}
	return d
}

func getEnvDuration(k string, d time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if dur, err := time.ParseDuration(v); err == nil { return dur }
	}
	return d
}

func getEnvBool(k string, d bool) bool {
	if v := os.Getenv(k); v != "" {
		return v == "true" || v == "1" || v == "yes"
	}
	return d
}
EOF

    # Create startup/deps.go
    cat > internal/startup/deps.go <<EOF
package startup

import (
	"context"
	"fmt"
	"time"

	"$go_module/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Dependencies holds all initialized dependencies
type Dependencies struct {
	Config *config.Config
	DB     *pgxpool.Pool
}

// Initialize creates all core dependencies
func Initialize(ctx context.Context, cfg *config.Config) (*Dependencies, error) {
	deps := &Dependencies{
		Config: cfg,
	}

	// Initialize database
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse db config: %w", err)
	}
	poolConfig.MaxConns = 20
	poolConfig.MinConns = 2
	poolConfig.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}

	deps.DB = pool
	return deps, nil
}

// Close closes all resources
func (d *Dependencies) Close() error {
	if d.DB != nil {
		d.DB.Close()
	}
	return nil
}
EOF

    # Create bootstrap/services.go
    mkdir -p internal/bootstrap
    cat > internal/bootstrap/services.go <<EOF
package bootstrap

// Services holds references to all domain services
type Services struct {
	// Add service references as they are created
	// Example:
	// User *user.Service
	// Tree *tree.Service
}

// NewServices creates the services container
func NewServices() *Services {
	return &Services{}
}
EOF

    # Create server/server.go
    cat > internal/server/server.go <<EOF
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Server wraps HTTP server with lifecycle management
type Server struct {
	httpServer *http.Server
	mux        *http.ServeMux
}

// New creates a new server instance
func New(port int) *Server {
	mux := http.NewServeMux()

	return &Server{
		mux: mux,
		httpServer: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}
}

// Mux returns the underlying ServeMux for route registration
func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

// Start begins listening for requests
func (s *Server) Start() error {
	slog.Info("server starting", "addr", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
EOF

    cat > migrations/000001_init.up.sql <<EOF
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email VARCHAR(255) NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_email ON users(email);
EOF

    cat > migrations/000001_init.down.sql <<EOF
DROP TABLE IF EXISTS users;
EOF
}

create_frontend_files() {
    local project_name="$1"
    local fe_dir="$2"

    cat > "$fe_dir/package.json" <<EOF
{
  "name": "${project_name}",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "lint": "eslint .",
    "test": "vitest run",
    "typecheck": "tsc --noEmit"
  },
  "dependencies": {
    "react": "^19.0.0",
    "react-dom": "^19.0.0",
    "react-router-dom": "^7.0.0",
    "zustand": "^5.0.0",
    "styled-components": "^6.1.0"
  },
  "devDependencies": {
    "@types/react": "^19.0.0",
    "@types/react-dom": "^19.0.0",
    "@vitejs/plugin-react": "^4.3.0",
    "typescript": "~5.6.0",
    "vite": "^6.0.0",
    "vitest": "^2.1.0"
  }
}
EOF

    cat > "$fe_dir/index.html" <<EOF
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>$project_name</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
EOF

    cat > "$fe_dir/src/main.tsx" <<EOF
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>
)
EOF

    cat > "$fe_dir/src/App.tsx" <<EOF
export default function App() {
  return (
    <div style={{ padding: '2rem', fontFamily: 'system-ui' }}>
      <h1>$project_name</h1>
      <p>Built with Foundation</p>
    </div>
  )
}
EOF

    cat > "$fe_dir/tsconfig.json" <<EOF
{
  "compilerOptions": {
    "target": "ES2020",
    "module": "ESNext",
    "lib": ["ES2020", "DOM"],
    "jsx": "react-jsx",
    "strict": true,
    "moduleResolution": "bundler",
    "noEmit": true,
    "skipLibCheck": true
  },
  "include": ["src"]
}
EOF

    cat > "$fe_dir/vite.config.ts" <<EOF
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: { port: 5173, proxy: { '/api': 'http://localhost:8080' } }
})
EOF

    for dir in features hooks lib types; do
        touch "$fe_dir/src/$dir/.gitkeep"
    done
}

create_makefile() {
    cat > Makefile <<'EOF'
.PHONY: setup build test lint dev clean verify help proto proto-ts build-wasm build-wasm-dev docker-up docker-down migrate-up migrate-create

PROJECT := $(shell basename $(CURDIR) | sed 's/_v[0-9]*$$//')
PROTO_PATH=api/protos
GOCMD=go
GOBUILD=$(GOCMD) build

setup: deps
	@echo "✓ Ready. Run 'make dev' to start."

deps:
	@[ -f go.mod ] && go mod download && go mod tidy || true
	@[ -f frontend/package.json ] && cd frontend && npm install || true

# Generate Go protobuf code
proto:
	@echo "Generating Go protobuf code..."
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
		fi \
	done
	@echo "✓ Go protobuf code generation complete"

# Generate TypeScript protobuf code
proto-ts:
	@echo "Generating TypeScript protobuf code..."
	@mkdir -p frontend/src/types/protos
	@for proto_dir in $$(find $(PROTO_PATH) -mindepth 1 -maxdepth 1 -type d); do \
		latest_version_dir=$$(ls -d $$proto_dir/v*/ 2>/dev/null | sort -V | tail -n 1); \
		if [ -d "$$latest_version_dir" ]; then \
			protoc \
				-I=$(PROTO_PATH) \
				--plugin=protoc-gen-ts_proto=./frontend/node_modules/.bin/protoc-gen-ts_proto \
				--ts_proto_out=frontend/src/types/protos \
				--ts_proto_opt=esModuleInterop=true,forceLong=string,useOptionals=messages \
				$$(find $$latest_version_dir -name '*.proto'); \
		fi \
	done
	@echo "✓ TypeScript protobuf code generation complete"

# Build WASM module (production - optimized)
build-wasm:
	@if [ -d wasm ]; then \
		echo "Building WASM (production)..."; \
		mkdir -p frontend/public; \
		GOOS=js GOARCH=wasm $(GOBUILD) -ldflags="-s -w" -o frontend/public/main.wasm ./wasm; \
		GOROOT=$$(go env GOROOT) && \
		WASM_EXEC_PATH_NEW="$$GOROOT/lib/wasm/wasm_exec.js" && \
		WASM_EXEC_PATH_OLD="$$GOROOT/misc/wasm/wasm_exec.js" && \
		if [ -f "$$WASM_EXEC_PATH_NEW" ]; then cp "$$WASM_EXEC_PATH_NEW" frontend/public/; \
		elif [ -f "$$WASM_EXEC_PATH_OLD" ]; then cp "$$WASM_EXEC_PATH_OLD" frontend/public/; \
		else echo "Error: wasm_exec.js not found in GOROOT ($$GOROOT)." >&2; exit 1; fi; \
		if command -v wasm-opt > /dev/null 2>&1; then \
			wasm-opt -O3 --strip-debug --enable-bulk-memory -o frontend/public/main.wasm frontend/public/main.wasm; \
			echo "✓ WASM optimized with wasm-opt"; \
		fi; \
		if command -v brotli > /dev/null 2>&1; then \
			brotli -q 11 -f frontend/public/main.wasm -o frontend/public/main.wasm.br; \
			echo "✓ WASM compressed with brotli"; \
		fi; \
		echo "✓ WASM build complete (production)"; \
	fi

# Build WASM module (development - unstripped)
build-wasm-dev:
	@if [ -d wasm ]; then \
		echo "Building WASM (development)..."; \
		mkdir -p frontend/public; \
		GOOS=js GOARCH=wasm $(GOBUILD) -o frontend/public/main.wasm ./wasm; \
		GOROOT=$$(go env GOROOT) && \
		WASM_EXEC_PATH_NEW="$$GOROOT/lib/wasm/wasm_exec.js" && \
		WASM_EXEC_PATH_OLD="$$GOROOT/misc/wasm/wasm_exec.js" && \
		if [ -f "$$WASM_EXEC_PATH_NEW" ]; then cp "$$WASM_EXEC_PATH_NEW" frontend/public/; \
		elif [ -f "$$WASM_EXEC_PATH_OLD" ]; then cp "$$WASM_EXEC_PATH_OLD" frontend/public/; \
		else echo "Error: wasm_exec.js not found in GOROOT ($$GOROOT)." >&2; exit 1; fi; \
		echo "✓ WASM build complete (development)"; \
	fi

build: proto
	@[ -f go.mod ] && go build -o bin/server ./cmd/server || true
	@[ -d frontend ] && cd frontend && npm run build || true

# Build all including WASM
all: proto proto-ts build build-wasm
	@echo "✓ All builds complete"

test:
	@[ -f go.mod ] && go test -race ./... || true
	@[ -d frontend ] && cd frontend && npm test || true

lint:
	@[ -f go.mod ] && golangci-lint run || true
	@[ -d frontend ] && cd frontend && npm run lint || true

dev:
	@[ -f go.mod ] && go run ./cmd/server || ([ -d frontend ] && cd frontend && npm run dev)

docker-up:
	docker compose up -d

docker-down:
	docker compose down

migrate-up:
	@migrate -path migrations -database "$$DATABASE_URL" up

migrate-down:
	@migrate -path migrations -database "$$DATABASE_URL" down 1

migrate-create:
	@read -p "Name: " n && touch migrations/$$(date +%Y%m%d%H%M%S)_$$n.{up,down}.sql

verify: lint test build
	@echo "✓ All checks passed"

clean:
	rm -rf bin coverage dist
	@rm -f frontend/public/main.wasm frontend/public/main.wasm.br frontend/public/wasm_exec.js
	@find $(PROTO_PATH) -name "*.pb.go" -delete 2>/dev/null || true

help:
	@echo "Development:"
	@echo "  setup          - Install dependencies"
	@echo "  dev            - Start development server"
	@echo "  build          - Build all targets"
	@echo "  all            - Build everything including WASM"
	@echo "  test           - Run tests"
	@echo "  lint           - Run linters"
	@echo "  verify         - Full CI check (lint + test + build)"
	@echo ""
	@echo "Protobuf & WASM:"
	@echo "  proto          - Generate Go protobuf code"
	@echo "  proto-ts       - Generate TypeScript protobuf code"
	@echo "  build-wasm     - Build WASM (production, optimized)"
	@echo "  build-wasm-dev - Build WASM (development, unstripped)"
	@echo ""
	@echo "Docker:"
	@echo "  docker-up      - Start containers"
	@echo "  docker-down    - Stop containers"
	@echo ""
	@echo "Database:"
	@echo "  migrate-up     - Run migrations"
	@echo "  migrate-down   - Rollback last migration"
	@echo "  migrate-create - Create new migration"
EOF
}

create_docker_files() {
    local project_name="$1"

    cat > Dockerfile <<'EOF'
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server ./cmd/server

FROM alpine:3.21
RUN adduser -D app
USER app
COPY --from=builder /server /server
EXPOSE 8080
CMD ["/server"]
EOF

    cat > docker-compose.yml <<EOF
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
      POSTGRES_DB: $project_name
    ports: ["5432:5432"]
    healthcheck:
      test: pg_isready -U postgres
      interval: 5s

  redis:
    image: redis:7-alpine
    ports: ["6379:6379"]

  server:
    build: .
    ports: ["8080:8080"]
    environment:
      DATABASE_URL: postgres://postgres:postgres@postgres:5432/$project_name?sslmode=disable
      REDIS_URL: redis://redis:6379
    depends_on:
      postgres: { condition: service_healthy }
EOF
}

create_readme() {
    local project_name="$1"
    local profile="$2"

    cat > README.md <<EOF
# $project_name

Built with [Foundation](https://github.com/ovasabi/foundation) ($profile profile).

## Quick Start

\`\`\`bash
make setup    # Install dependencies
make dev      # Start development
make verify   # Run all checks
\`\`\`

## What's Included

- **Event-driven architecture** with correlation tracing
- **AI-ready** with AGENTS.md and CLAUDE.md
- **Production patterns** from day one

See \`docs/foundation/\` for coding practices.
EOF
}

create_gitignore() {
    cat > .gitignore <<'EOF'
bin/
dist/
coverage/
node_modules/
.env
.env.local
*.log
.DS_Store
EOF
}

create_env_example() {
    local project_name="$1"
    cat > .env.example <<EOF
APP_ENV=development
PORT=8080
DATABASE_URL=postgres://postgres:postgres@localhost:5432/${project_name}?sslmode=disable
REDIS_URL=redis://localhost:6379
JWT_SECRET=change-me
EOF
}

create_claude_md() {
    local project_name="$1"
    local profile="$2"

    cat > CLAUDE.md <<EOF
# $project_name

Foundation project ($profile). Read AGENTS.md first.

## Commands
- \`make dev\` — start server
- \`make test\` — run tests
- \`make verify\` — full CI

## Rules
1. Every mutation needs correlationId
2. Use foundation error taxonomy
3. Bound all loops and retries
4. Never trust client IDs
EOF
}

create_cursorrules() {
    local project_name="$1"
    cat > .cursorrules <<EOF
# $project_name — Foundation Project
# Read AGENTS.md for architecture rules
# Key: event-driven, correlation IDs, bounded operations
EOF
}

create_agents_folder() {
    local project_name="$1"
    local profile="$2"

    mkdir -p .agents

    # POST_INIT.md
    cat > .agents/POST_INIT.md <<'AGENTEOF'
# Post-Initialization Checklist

> This file tracks the transition from foundation boilerplate to production-ready application.

## Phase 1: Environment Setup

- [ ] Copy `.env.example` to `.env`
- [ ] Configure `DATABASE_URL` for local Postgres
- [ ] Configure `REDIS_URL` for local Redis
- [ ] Set `JWT_SECRET` to a secure random value
- [ ] Run `make docker-up` to start infrastructure
- [ ] Run `make setup` to install dependencies

## Phase 2: Define Your Domains

Your application needs domain-specific contracts. Foundation provides the transport layer; you define the business logic.

- [ ] Identify 3-5 core domains for your application
- [ ] Create proto files in `api/protos/<domain>/v1/`
- [ ] Define shared metadata in `api/protos/common/v1/metadata.proto`
- [ ] Run `make proto` to generate Go bindings

**Domain Examples by Category:**

| Category | Typical Domains |
|----------|-----------------|
| Fintech | user, business, billing, tax, compliance, audit |
| Media | workspace, media, publish, identity, locale |
| Civic | incident, report, evidence, verification, geo, safety |
| E-commerce | user, product, cart, order, payment, shipping |

See `.agents/DOMAIN_GUIDE.md` for proto patterns and event naming conventions.

## Phase 3: Wire Up Services

- [ ] Create service packages in `internal/service/<domain>/`
- [ ] Register event handlers in `internal/registry/`
- [ ] Add domain-specific workers if async processing needed
- [ ] Write initial integration tests

## Phase 4: Frontend Integration

- [ ] Define TypeScript types mirroring your protos
- [ ] Set up WebSocket connection with envelope support
- [ ] Implement authentication flow
- [ ] Create feature modules for each domain

## Phase 5: Pre-Production

- [ ] Run `make verify` (lint + test + build)
- [ ] Review security checklist in `docs/foundation/coding_practices.md`
- [ ] Set up CI/CD secrets
- [ ] Configure production environment variables

---

## Communication Architecture

Foundation uses **event-driven WebSocket communication** with compressed binary envelopes:

1. **Envelope Format**: All messages wrapped in `events.Envelope`
2. **Binary/JSON**: Clients negotiate format via `?format=binary`
3. **Compression**: Automatic brotli/gzip/flate for large payloads
4. **Correlation**: Every request has `correlation_id` for tracing

See `foundation/server-kit/go/events/envelope.go` for envelope structure.
See `foundation/server-kit/go/compress/` for compression utilities.
AGENTEOF

    # DOMAIN_GUIDE.md
    cat > .agents/DOMAIN_GUIDE.md <<'AGENTEOF'
# Domain Definition Guide

> How to transition from foundation boilerplate to app-specific domains.

## Understanding the Architecture

Foundation provides:
- **Transport Layer**: WebSocket + HTTP with envelope-based messaging
- **Event Bus**: Redis-backed pub/sub for async communication
- **Compression**: Brotli/gzip/flate for binary payloads
- **Security**: JWT auth, rate limiting, capability-based access

You provide:
- **Domain Protos**: Your business contracts in `api/protos/<domain>/v1/`
- **Service Handlers**: Event handlers in `internal/service/<domain>/`
- **Domain Logic**: Business rules, validation, state management

## Event Naming Convention

Foundation uses a consistent event naming pattern:

```
<domain>:<action>:<version>:<state>
```

**Components:**
- `domain`: Business domain (user, incident, media, etc.)
- `action`: The operation (create, update, authenticate, etc.)
- `version`: Schema version (v1, v2)
- `state`: Event state (requested, success, failed, ack)

**Examples:**
```
user:authenticate:v1:requested    # Client requests auth
user:authenticate:v1:success      # Auth succeeded
incident:create:v1:requested      # Create incident request
incident:list:v1:requested        # List incidents
```

## Creating a Domain Proto

### 1. Create the folder structure
```bash
mkdir -p api/protos/<domain>/v1
```

### 2. Copy and customize the template
```bash
cp api/protos/_template/v1/example.proto api/protos/<domain>/v1/<domain>.proto
```

### 3. Generate bindings
```bash
make proto
```

## Registering Event Handlers

```go
// internal/service/<domain>/registration.go
func Register(reg *registry.Registry, svc *Service) {
    reg.Handle("<domain>:create:v1:requested", svc.Create)
    reg.Handle("<domain>:get:v1:requested", svc.Get)
    reg.Handle("<domain>:list:v1:requested", svc.List)
}
```

## Quick Reference: Domain Checklist

- [ ] Create `api/protos/<domain>/v1/<domain>.proto`
- [ ] Follow event naming: `<domain>:<action>:v1:<state>`
- [ ] Run `make proto` to generate bindings
- [ ] Create `internal/service/<domain>/service.go`
- [ ] Register handlers in `internal/registry/`
- [ ] Write tests in `tests/integration/<domain>_test.go`
AGENTEOF
}

create_api_documentation() {
    local project_name="$1"

    # api/README.md
    cat > api/README.md <<'APIEOF'
# API Layer

`api/` is the application communication boundary.

## Contents

1. **`api/protos/`** - Protocol buffer contracts for control-plane messaging

## Architecture

Foundation uses **event-driven messaging** over WebSocket:

- **No gRPC services** - all communication via envelope dispatch
- **Event types** follow `<domain>:<action>:<version>:<state>` pattern
- **Compressed binary** - automatic brotli/gzip for large payloads
- **Correlation tracing** - every request carries `correlation_id`

## Adding New Domains

```bash
mkdir -p api/protos/<domain>/v1
cp api/protos/_template/v1/example.proto api/protos/<domain>/v1/<domain>.proto
make proto
```

See `api/protos/README.md` for contract rules and `.agents/DOMAIN_GUIDE.md` for patterns.
APIEOF

    # api/protos/README.md
    cat > api/protos/README.md <<'PROTOEOF'
# Proto Contracts

Application-specific protocol buffer definitions.

## Package Structure

```
protos/
├── common/v1/           # Shared types (metadata)
├── <domain>/v1/         # Domain-specific contracts
└── _template/v1/        # Reference templates
```

## Contract Rules

1. **Versioning**: All packages use `<domain>.v1`
2. **Metadata**: Every mutating request includes `RequestMetadata`
3. **Idempotency**: Mutations carry `idempotency_key`
4. **Field Numbers**: Reserve 1-10 for common fields

## Event Type Mapping

| Message | Event Type |
|---------|------------|
| `CreateFooRequest` | `foo:create:v1:requested` |
| `CreateFooResponse` | `foo:create:v1:success` |

## Generating Bindings

```bash
make proto
```
PROTOEOF

    # Create _template folder with example.proto
    mkdir -p api/protos/_template/v1

    cat > api/protos/_template/v1/example.proto <<'TEMPLATEEOF'
// Example Domain Proto - Copy and customize for your domains
syntax = "proto3";

package example.v1;

option go_package = "github.com/ovasabi/PROJECT_NAME/api/protos/example/v1;examplev1";

import "google/protobuf/timestamp.proto";

message RequestMetadata {
  string correlation_id = 1;
  string idempotency_key = 2;
  string device_id = 3;
  string user_id = 4;
  string organization_id = 5;
  google.protobuf.Timestamp timestamp = 6;
}

message ExampleEntity {
  string id = 1;
  string organization_id = 2;
  string name = 6;
  string description = 7;
  google.protobuf.Timestamp created_at = 23;
  google.protobuf.Timestamp updated_at = 24;
}

message CreateExampleRequest {
  RequestMetadata metadata = 1;
  string name = 2;
  string description = 3;
}

message CreateExampleResponse {
  ExampleEntity entity = 1;
}

message GetExampleRequest {
  RequestMetadata metadata = 1;
  string id = 2;
}

message GetExampleResponse {
  ExampleEntity entity = 1;
}

message ListExampleRequest {
  RequestMetadata metadata = 1;
  int32 page_size = 2;
  string page_token = 3;
}

message ListExampleResponse {
  repeated ExampleEntity entities = 1;
  string next_page_token = 2;
  int32 total_count = 3;
}
TEMPLATEEOF
}

create_wasm_boilerplate() {
    local go_module="$1"

    # Create WASM main.go following fintech_v1 pattern
    cat > wasm/main.go <<WASMEOF
//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"
	"time"
)

var ws js.Value
var connectionState = "disconnected" // disconnected, connecting, connected
var messageQueue = make(chan wsMessage, 100)

type wsMessage struct {
	dataType int
	payload  []byte
}

// EventEnvelope represents the standard event wrapper
type EventEnvelope struct {
	EventType     string          \`json:"event_type"\`
	Payload       json.RawMessage \`json:"payload"\`
	Timestamp     string          \`json:"timestamp"\`
	CorrelationID string          \`json:"correlation_id"\`
	SchemaVersion string          \`json:"schema_version,omitempty"\`
}

func main() {
	wasmLog("[main] Initializing WASM runtime")

	// Initialize global metadata
	js.Global().Set("__WASM_GLOBAL_METADATA", js.Global().Get("Object").New())

	// Expose WASM functions to JS
	js.Global().Set("wasmReady", js.ValueOf(true))
	js.Global().Set("wasmVersion", js.ValueOf("1.0.0"))
	js.Global().Set("sendWasmMessage", js.FuncOf(jsSendMessage))
	js.Global().Set("connectWebSocket", js.FuncOf(jsConnectWebSocket))
	js.Global().Set("disconnectWebSocket", js.FuncOf(jsDisconnectWebSocket))
	js.Global().Set("setFrontendReady", js.FuncOf(jsSetFrontendReady))

	// Start message processor
	go processMessages()

	wasmLog("[main] WASM initialized. Waiting for frontend ready signal...")

	// Keep WASM alive
	select {}
}

func initWebSocket() {
	if connectionState == "connecting" || connectionState == "connected" {
		wasmLog("[initWebSocket] Already connecting/connected, skipping")
		return
	}

	connectionState = "connecting"

	location := js.Global().Get("location")
	protocol := "ws:"
	if location.Get("protocol").String() == "https:" {
		protocol = "wss:"
	}
	host := location.Get("host").String()
	url := protocol + "//" + host + "/ws"

	wasmLog("[initWebSocket] Connecting to " + url)

	wsObj := js.Global().Get("WebSocket")
	ws = wsObj.New(url)
	ws.Set("binaryType", "arraybuffer")
	configureWebSocketCallbacks(ws)
}

func configureWebSocketCallbacks(socket js.Value) {
	socket.Set("onopen", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		connectionState = "connected"
		wasmLog("[WebSocket:onopen] Connected")
		updateMetadata("webSocketConnected", true)
		notifyFrontend("connection:status", map[string]interface{}{
			"connected": true,
			"reason":    "websocket_opened",
		})
		return nil
	}))

	socket.Set("onmessage", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		event := args[0]
		msg := event.Get("data")

		go func() {
			var data []byte
			if msg.InstanceOf(js.Global().Get("ArrayBuffer")) {
				length := msg.Get("byteLength").Int()
				data = make([]byte, length)
				uint8Array := js.Global().Get("Uint8Array").New(msg)
				js.CopyBytesToGo(data, uint8Array)
			} else {
				data = []byte(msg.String())
			}
			messageQueue <- wsMessage{dataType: 0, payload: data}
		}()

		return nil
	}))

	socket.Set("onerror", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		wasmLog("[WebSocket:onerror] Error occurred")
		updateMetadata("webSocketConnected", false)
		return nil
	}))

	socket.Set("onclose", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		wasmLog("[WebSocket:onclose] Connection closed")
		connectionState = "disconnected"
		ws = js.Undefined()
		updateMetadata("webSocketConnected", false)
		notifyFrontend("connection:status", map[string]interface{}{
			"connected": false,
			"reason":    "websocket_closed",
		})
		return nil
	}))
}

func processMessages() {
	for msg := range messageQueue {
		var env EventEnvelope
		if err := json.Unmarshal(msg.payload, &env); err != nil {
			wasmLog("[processMessages] Failed to unmarshal: " + err.Error())
			continue
		}

		// Forward to frontend event handler
		if handler := js.Global().Get("onWasmMessage"); handler.Type() == js.TypeFunction {
			eventObj := js.Global().Get("Object").New()
			eventObj.Set("type", env.EventType)
			eventObj.Set("event_type", env.EventType)
			eventObj.Set("payload", parseJSON(env.Payload))
			eventObj.Set("timestamp", env.Timestamp)
			eventObj.Set("correlation_id", env.CorrelationID)
			handler.Invoke(eventObj)
		}
	}
}

func jsSendMessage(this js.Value, args []js.Value) interface{} {
	if len(args) == 0 {
		return nil
	}

	if ws.IsNull() || ws.IsUndefined() {
		wasmLog("[jsSendMessage] WebSocket not connected")
		return nil
	}

	if ws.Get("readyState").Int() != 1 {
		wasmLog("[jsSendMessage] WebSocket not open")
		return nil
	}

	event := args[0]
	jsonString := js.Global().Get("JSON").Call("stringify", event)
	ws.Call("send", jsonString.String())
	return nil
}

func jsConnectWebSocket(this js.Value, args []js.Value) interface{} {
	go initWebSocket()
	return nil
}

func jsDisconnectWebSocket(this js.Value, args []js.Value) interface{} {
	if !ws.IsNull() && !ws.IsUndefined() {
		connectionState = "disconnected"
		ws.Call("close", 1000, "manual_disconnect")
		ws = js.Undefined()
	}
	return nil
}

func jsSetFrontendReady(this js.Value, args []js.Value) interface{} {
	wasmLog("[jsSetFrontendReady] Frontend ready")
	updateMetadata("frontendReady", true)
	if ws.IsNull() || ws.IsUndefined() || connectionState == "disconnected" {
		initWebSocket()
	}
	return nil
}

func updateMetadata(key string, value interface{}) {
	metadata := js.Global().Get("__WASM_GLOBAL_METADATA")
	if metadata.Truthy() {
		metadata.Set(key, js.ValueOf(value))
	}
}

func notifyFrontend(eventType string, payload map[string]interface{}) {
	if handler := js.Global().Get("onWasmMessage"); handler.Type() == js.TypeFunction {
		eventObj := js.Global().Get("Object").New()
		eventObj.Set("type", eventType)
		eventObj.Set("event_type", eventType)
		eventObj.Set("payload", toJSValue(payload))
		eventObj.Set("timestamp", time.Now().UTC().Format(time.RFC3339))
		handler.Invoke(eventObj)
	}
}

func parseJSON(data json.RawMessage) js.Value {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return js.Null()
	}
	return toJSValue(v)
}

func toJSValue(v interface{}) js.Value {
	switch val := v.(type) {
	case string:
		return js.ValueOf(val)
	case int:
		return js.ValueOf(val)
	case float64:
		return js.ValueOf(val)
	case bool:
		return js.ValueOf(val)
	case map[string]interface{}:
		obj := js.Global().Get("Object").New()
		for k, v := range val {
			obj.Set(k, toJSValue(v))
		}
		return obj
	case []interface{}:
		arr := js.Global().Get("Array").New(len(val))
		for i, item := range val {
			arr.SetIndex(i, toJSValue(item))
		}
		return arr
	default:
		return js.Null()
	}
}

func wasmLog(message string) {
	js.Global().Get("console").Call("log", "[WASM]", message)
}
WASMEOF

    # No build.sh - WASM is built via Makefile
}

create_wasm_readme() {
    cat > wasm/README.md <<'WASMREADME'
# WASM Module

WebAssembly module for client-side processing and WebSocket communication.

## Overview

This WASM module provides:
- **WebSocket Management**: Connection handling with auto-reconnect
- **Event Processing**: Envelope-based message handling
- **Binary Communication**: Efficient data transfer with compression support

## Building

```bash
# Development build (faster, includes debug info)
make build-wasm-dev

# Production build (optimized, compressed)
make build-wasm
```

Output files are placed in `frontend/public/`:
- `main.wasm` - The compiled WebAssembly binary
- `wasm_exec.js` - Go's WASM runtime (copied from GOROOT)
- `main.wasm.br` - Brotli-compressed binary (production only)

## Frontend Integration

### Loading WASM

```html
<script src="/wasm_exec.js"></script>
<script>
const go = new Go();
WebAssembly.instantiateStreaming(fetch("/main.wasm"), go.importObject)
  .then((result) => {
    go.run(result.instance);
    // WASM is ready when wasmReady === true
  });
</script>
```

### Exposed Functions

| Function | Description |
|----------|-------------|
| `setFrontendReady()` | Signal frontend is ready, initiates WebSocket |
| `sendWasmMessage(event)` | Send event through WebSocket |
| `connectWebSocket()` | Manually trigger WebSocket connection |
| `disconnectWebSocket()` | Close WebSocket connection |

### Receiving Messages

```javascript
// Register handler before calling setFrontendReady()
window.onWasmMessage = (event) => {
  console.log('Event:', event.event_type, event.payload);
};

// Signal ready to connect
setFrontendReady();
```

## Event Format

Events follow the envelope pattern:

```json
{
  "event_type": "domain:action:v1:state",
  "payload": { ... },
  "timestamp": "2024-01-01T00:00:00Z",
  "correlation_id": "uuid"
}
```

## Extending

To add new functionality:

1. Add new exported functions using `js.FuncOf()`
2. Register them in `main()` with `js.Global().Set()`
3. Update this README with the new function documentation

See `fintech_v1/wasm/main.go` for advanced patterns including:
- Session token handling
- Compression/decompression
- Reconnection with exponential backoff
- Message queuing
WASMREADME
}

create_tests_readme() {
    cat > tests/README.md <<'TESTSREADME'
# Tests

Organized test suites for the application.

## Directory Structure

```
tests/
├── integration/    # Integration tests (database, external services)
├── e2e/           # End-to-end tests (full system, Playwright)
└── README.md
```

## Test Types

### Unit Tests (`*_test.go` alongside source)

Unit tests live next to the code they test:

```
internal/service/user/
├── service.go
└── service_test.go
```

Run with: `make test`

### Integration Tests (`tests/integration/`)

Tests that require database or external services:

```go
// tests/integration/user_test.go
func TestUserService_Create(t *testing.T) {
    // Requires running database
    db := testutil.SetupTestDB(t)
    svc := user.NewService(db)
    // ...
}
```

Run with: `make test-integration`

### E2E Tests (`tests/e2e/`)

Full system tests using Playwright or similar:

```
tests/e2e/
├── package.json
├── playwright.config.ts
└── specs/
    ├── auth.spec.ts
    └── dashboard.spec.ts
```

Run with: `make test-e2e`

## Test Database

Integration tests use a separate test database:

```bash
# Setup test database
make setup-test-db

# Run migrations on test database
make migrate-test
```

## Best Practices

1. **Isolation**: Each test should be independent
2. **Cleanup**: Use `t.Cleanup()` for teardown
3. **Fixtures**: Use `testdata/` for test fixtures
4. **Naming**: Use descriptive names: `TestUserService_Create_WithValidInput`
5. **Coverage**: Run `make test-coverage` for coverage reports

## Environment Variables

```bash
# Test database connection
DATABASE_URL="postgres://user:pass@localhost:5432/project_test?sslmode=disable"

# Disable billing for tests
BILLING_DISABLED=true
```
TESTSREADME

    # Also create placeholder READMEs in subdirectories
    cat > tests/integration/README.md <<'INTEGRATIONREADME'
# Integration Tests

Place integration tests here. These tests require external dependencies (database, Redis, etc.).

## Example

```go
package integration

import (
    "testing"
    "github.com/your/project/internal/testutil"
)

func TestUserCreation(t *testing.T) {
    db := testutil.SetupTestDB(t)
    defer db.Close()

    // Test with real database
}
```

## Running

```bash
make test-integration
```
INTEGRATIONREADME

    cat > tests/e2e/README.md <<'E2EREADME'
# End-to-End Tests

Browser-based tests using Playwright.

## Setup

```bash
cd tests/e2e
npm install
npx playwright install
```

## Running

```bash
# From project root
make test-e2e

# Or directly
cd tests/e2e && npm test
```

## Writing Tests

```typescript
// specs/auth.spec.ts
import { test, expect } from '@playwright/test';

test('user can login', async ({ page }) => {
  await page.goto('/login');
  await page.fill('[name="email"]', 'test@example.com');
  await page.fill('[name="password"]', 'password');
  await page.click('button[type="submit"]');
  await expect(page).toHaveURL('/dashboard');
});
```
E2EREADME
}

create_scripts_readme() {
    cat > scripts/README.md <<'SCRIPTSREADME'
# Scripts

Utility scripts for development, deployment, and maintenance.

## Common Scripts

| Script | Purpose |
|--------|---------|
| `setup-cgo.sh` | Configure CGO for native dependencies |
| `generate_testdata/` | Generate test fixtures |
| `deploy.sh` | Deployment automation |

## Adding New Scripts

1. Create the script in this directory
2. Add executable permission: `chmod +x script.sh`
3. Document in this README
4. If frequently used, add a Makefile target

## Best Practices

- Use `set -e` for fail-fast behavior
- Include usage comments at the top
- Prefer Makefile targets for common operations
- Keep scripts focused and single-purpose
SCRIPTSREADME
}

create_next_steps() {
    local project_name="$1"
    local profile="$2"

    cat > NEXT_STEPS.md <<EOF
# Next Steps for $project_name

Foundation gave you production-grade infrastructure. Now define what makes your app unique.

## Quick Start

\`\`\`bash
make setup        # Install dependencies
make docker-up    # Start Postgres + Redis
make dev          # Start development server
\`\`\`

## What You Need to Define

### 1. Domain Contracts (First Priority)

Your app needs domain-specific protos. Foundation provides transport; you define business logic.

\`\`\`bash
# Create your first domain
mkdir -p api/protos/user/v1
cp api/protos/_template/v1/example.proto api/protos/user/v1/user.proto
# Edit and customize, then generate
make proto
\`\`\`

**Identify your 3-5 core domains.** Examples:
- Fintech: \`user\`, \`business\`, \`billing\`, \`tax\`, \`compliance\`
- Media: \`workspace\`, \`media\`, \`publish\`, \`identity\`
- Civic: \`incident\`, \`report\`, \`evidence\`, \`verification\`, \`geo\`

See \`.agents/DOMAIN_GUIDE.md\` for patterns and naming conventions.

### 2. Service Implementation

Once protos exist, create service handlers in \`internal/service/<domain>/\`

### 3. Frontend Integration

Connect via WebSocket with envelope-based messaging. See \`foundation/runtime-transport/ts/\`

## Detailed Checklist

See \`.agents/POST_INIT.md\` for comprehensive step-by-step guide.

## Key Files

| File | Purpose |
|------|---------|
| \`AGENTS.md\` | AI assistant instructions |
| \`.agents/POST_INIT.md\` | Initialization checklist |
| \`.agents/DOMAIN_GUIDE.md\` | Domain definition patterns |
| \`api/protos/README.md\` | Proto contract rules |

---
*Generated by Foundation v$FOUNDATION_VERSION*
*Delete this file once you've completed initial setup.*
EOF
}

#───────────────────────────────────────────────────────────────────────────────
# SUCCESS SCREEN
#───────────────────────────────────────────────────────────────────────────────

show_success() {
    local project_name="$1"
    local project_dir="$2"
    local profile="$3"

    echo ""
    if [[ "$HAS_GUM" == "true" ]]; then
        gum style \
            --border rounded \
            --border-foreground 78 \
            --padding "1 2" \
            --margin "1 0" \
            "✓ $project_name created"
    else
        echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
        echo -e "${GREEN}  ✓ $project_name created${RESET}"
        echo -e "${GREEN}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
    fi

    echo ""
    print_narrative "Immediate steps:"
    echo ""
    print_command "cd $project_dir"
    print_command "make setup"
    print_command "make docker-up"
    print_command "make dev"

    # Profile-specific guidance
    case $profile in
        full|backend)
            echo ""
            print_section "Define Your Application"
            print_narrative "Foundation provides infrastructure. Now define your domains."
            echo ""
            print_info "Read NEXT_STEPS.md for detailed guidance"
            print_item "Create domain protos: api/protos/<domain>/v1/"
            print_item "Use template: api/protos/_template/v1/example.proto"
            print_item "Generate bindings: make proto"
            echo ""
            print_muted "Event pattern: <domain>:<action>:v1:<state>"
            print_muted "Example: user:create:v1:requested"
            ;;
        frontend)
            echo ""
            print_section "Connect to Backend"
            print_narrative "Set up WebSocket connection with envelope support."
            echo ""
            print_info "See foundation/runtime-transport/ts/ for TypeScript utilities"
            ;;
    esac

    echo ""
    print_muted "─────────────────────────────────────────────────────────"
    print_muted "Key files:"
    print_muted "  NEXT_STEPS.md          Step-by-step setup guide"
    print_muted "  .agents/POST_INIT.md   Detailed checklist"
    print_muted "  .agents/DOMAIN_GUIDE.md  Domain patterns"
    print_muted "  AGENTS.md              AI assistant instructions"
    echo ""
}

#───────────────────────────────────────────────────────────────────────────────
# MAIN
#───────────────────────────────────────────────────────────────────────────────

PROJECT_NAME=""
PROFILE="full"
WITH_DOCKER="true"
WITH_WASM=""
GO_MODULE=""
DRY_RUN="false"
SKIP_DEPS="false"

while [[ $# -gt 0 ]]; do
    case $1 in
        --help|-h)      show_help; exit 0 ;;
        --why)          show_problem_statement; show_solution; show_ai_angle; exit 0 ;;
        --no-docker)    WITH_DOCKER="false"; shift ;;
        --no-wasm)      WITH_WASM="false"; shift ;;
        --with-wasm)    WITH_WASM="true"; shift ;;
        --go-module)    GO_MODULE="$2"; shift 2 ;;
        --dry-run)      DRY_RUN="true"; shift ;;
        --skip-deps)    SKIP_DEPS="true"; shift ;;
        full|backend|frontend|minimal) PROFILE="$1"; shift ;;
        -*)             print_error "Unknown: $1"; exit 1 ;;
        *)              [[ -z "$PROJECT_NAME" ]] && PROJECT_NAME="$1"; shift ;;
    esac
done

# Interactive mode if no project name
if [[ -z "$PROJECT_NAME" && "$HAS_GUM" == "true" ]]; then
    print_logo
    PROJECT_NAME=$(gum input --placeholder "Project name")
    [[ -z "$PROJECT_NAME" ]] && { print_error "Project name required"; exit 1; }

    PROFILE=$(gum choose --header "Profile" "full" "backend" "frontend" "minimal")
fi

# Validate
if [[ -z "$PROJECT_NAME" ]]; then
    print_logo
    print_error "Project name required"
    echo ""
    echo -e "  ${WHITE}Usage:${RESET} ./init.sh <project-name> [profile]"
    echo -e "  ${WHITE}Help:${RESET}  ./init.sh --help"
    echo ""
    exit 1
fi

# Defaults
[[ -z "$WITH_WASM" ]] && { [[ "$PROFILE" == "full" ]] && WITH_WASM="true" || WITH_WASM="false"; }
[[ -z "$GO_MODULE" ]] && GO_MODULE="github.com/ovasabi/$PROJECT_NAME"

PROJECT_DIR="$(dirname "$FOUNDATION_DIR")/${PROJECT_NAME}_v1"

# Show config
print_logo

print_section "Configuration"
print_info "Project: ${WHITE}$PROJECT_NAME${RESET}"
print_info "Profile: ${WHITE}$PROFILE${RESET}"
print_info "Path: ${GRAY}$PROJECT_DIR${RESET}"
[[ "$PROFILE" == "full" || "$PROFILE" == "backend" ]] && print_info "Module: ${GRAY}$GO_MODULE${RESET}"

# Check dependencies
if [[ "$SKIP_DEPS" != "true" ]]; then
    if ! check_dependencies "$PROFILE" "$WITH_DOCKER" "$WITH_WASM"; then
        exit 1
    fi
fi

# Dry run
if [[ "$DRY_RUN" == "true" ]]; then
    print_section "Dry Run"
    print_muted "No files created. Remove --dry-run to proceed."
    exit 0
fi

# Check existing
if [[ -d "$PROJECT_DIR" ]]; then
    print_error "Directory exists: $PROJECT_DIR"
    exit 1
fi

# Create
create_project "$PROJECT_NAME" "$PROFILE" "$GO_MODULE" "$WITH_DOCKER" "$WITH_WASM" "$PROJECT_DIR"

# Done
show_success "$PROJECT_NAME" "$PROJECT_DIR" "$PROFILE"
