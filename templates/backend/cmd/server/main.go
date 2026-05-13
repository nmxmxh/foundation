package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/auth"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"

	"{{MODULE_PATH}}/internal/config"
	"{{MODULE_PATH}}/internal/server"
	"{{MODULE_PATH}}/internal/startup"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx); err != nil {
		slog.Error("application error", "error", err)
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
	logger := startup.NewLogger(cfg.Env, cfg.LogLevel)
	slog.SetDefault(logger)

	slog.Info("starting server",
		"env", cfg.Env,
		"port", cfg.Port,
	)

	// Initialize dependencies
	deps, cleanup, err := startup.InitDependencies(ctx, cfg)
	if err != nil {
		return fmt.Errorf("init dependencies: %w", err)
	}
	defer cleanup()

	// Create and start server
	srv := server.New(cfg, deps.Registry, deps.Handler)
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
