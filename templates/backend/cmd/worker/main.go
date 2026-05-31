package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"{{MODULE_PATH}}/internal/config"
	"{{MODULE_PATH}}/internal/startup"
	"{{MODULE_PATH}}/internal/worker"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log := startup.NewLogger(cfg.Env, cfg.LogLevel).With("component", "worker")

	// Connect to database
	dbConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Error("failed to parse database config", "error", err)
		os.Exit(1)
	}
	database.ApplyPoolOptions(dbConfig, workerPoolOptions(cfg))

	dbPool, err := pgxpool.NewWithConfig(context.Background(), dbConfig)
	if err != nil {
		log.Error("unable to connect to database", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	// Verify database connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if pingErr := dbPool.Ping(ctx); pingErr != nil {
		cancel()
		log.ErrorContext(ctx, "unable to ping database", "error", pingErr)
		os.Exit(1)
	}
	cancel()
	log.Info("database connected")

	// Create context that cancels on interrupt signal
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	// Initialize River Workers
	workers := river.NewWorkers()
	worker.RegisterAll(workers, &worker.Dependencies{
		DB:     dbPool,
		Config: cfg,
		// Add your initialized services here
	})

	// Initialize River Client
	riverClient, err := river.NewClient(riverpgxv5.New(dbPool), &river.Config{
		Workers:      workers,
		Queues:       worker.DefaultQueueConfig(cfg),
		PeriodicJobs: worker.PeriodicJobs(cfg),
	})
	if err != nil {
		log.Error("failed to initialize River client", "error", err)
		os.Exit(1)
	}

	// Start River Client
	if err := riverClient.Start(ctx); err != nil {
		log.ErrorContext(ctx, "failed to start River client", "error", err)
		os.Exit(1)
	}

	// Handle OS signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.InfoContext(ctx, "worker started", "queues", worker.DefaultQueueConfig(cfg))

	// Wait for signal
	sig := <-sigChan
	log.Info("received shutdown signal", "signal", sig)

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := riverClient.Stop(shutdownCtx); err != nil {
		log.ErrorContext(shutdownCtx, "error stopping River client", "error", err)
	}

	log.Info("worker stopped")
}

func workerPoolOptions(cfg *config.Config) database.PoolOptions {
	opts := database.DefaultPoolOptionsFor(database.RuntimeLaneBackground)
	if cfg.DBMaxConns > 0 {
		opts.MaxConns = cfg.DBMaxConns
	}
	if cfg.DBMinConns > 0 {
		opts.MinConns = cfg.DBMinConns
	}
	opts.HealthCheckPeriod = cfg.DBHealthCheckPeriod
	opts.ConnectTimeout = cfg.DBConnectTimeout
	opts.QueryTimeout = cfg.DBQueryTimeout
	opts.AcquireTimeout = cfg.DBAcquireTimeout
	return opts
}
