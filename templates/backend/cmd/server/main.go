package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

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
	srv := server.New(cfg, deps, logger)

	return srv.Run(ctx)
}
