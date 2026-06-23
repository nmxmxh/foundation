package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpserver"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/projectiongw"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"

	"{{MODULE_PATH}}/internal/bootstrap"
	"{{MODULE_PATH}}/internal/config"
	"{{MODULE_PATH}}/internal/startup"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "application error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Initialize logger
	log := startup.NewLogger(cfg.Env, cfg.LogLevel).With("component", "server")

	log.InfoContext(ctx, "starting server",
		"env", cfg.Env,
		"port", cfg.Port,
	)

	// Initialize dependencies
	deps, cleanup, err := startup.InitDependencies(ctx, cfg)
	if err != nil {
		return fmt.Errorf("init dependencies: %w", err)
	}
	defer cleanup()

	// Create and start the foundation HTTP server (server-kit/go/httpserver). The
	// runtime wiring — health, dispatch, websocket, projection gateway, middleware
	// — is foundation-owned and module-synced; this project maps only its config
	// onto the narrow httpserver.Config.
	srv := httpserver.New(&httpserver.Config{
		Port:                        cfg.Port,
		AllowedOrigins:              cfg.AllowedOrigins,
		ProtectOperationalEndpoints: cfg.ProtectOperationalEndpoints,
	}, deps.Registry, deps.Handler)
	srv.SetHTTPRoutes((&bootstrap.Services{}).HTTPRoutes())

	// Mount the Hermes projection read path at /v1/projections/: scoped
	// snapshots (GET) and live delta streams (WS). Access is identity-scoped by
	// default (the tenant is the authenticated organization), and deltas flow
	// from the store apply observer, so the live loop works with the in-memory
	// driver too — no Redis projector required.
	if deps.Projected != nil {
		gateway, err := projectiongw.NewGatewayForProjectedStore(deps.Projected, 0)
		if err != nil {
			return fmt.Errorf("init projection gateway: %w", err)
		}
		defer gateway.Close()
		srv.ConfigureProjectionGateway(gateway.Handler(projectiongw.HandlerConfig{}))
	}

	if cfg.RequireAuth || cfg.ProtectOperationalEndpoints {
		jwtManager, err := auth.NewJWTManager(cfg.JWTSecret)
		if err != nil {
			return fmt.Errorf("init jwt manager: %w", err)
		}
		srv.ConfigureAuth(jwtManager, security.NewAuthorizer(nil), cfg.RequireAuth)
	}
	if deps.Resilience != nil {
		srv.ConfigureHealthChecks(
			deps.Resilience.HealthHandler(),
			deps.Resilience.LivenessHandler(),
			deps.Resilience.ReadinessHandler(),
		)
	}

	return srv.Run(ctx)
}
