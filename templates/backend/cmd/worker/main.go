package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"{{MODULE_PATH}}/internal/config"
	"{{MODULE_PATH}}/internal/worker"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize logger
	logLevel := slog.LevelInfo
	if cfg.IsDevelopment() {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// Connect to database
	dbConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to parse database config: %v", err)
	}
	dbConfig.MaxConns = 25
	dbConfig.MinConns = 5
	dbConfig.HealthCheckPeriod = 30 * time.Second
	dbConfig.ConnConfig.ConnectTimeout = 5 * time.Second

	dbPool, err := pgxpool.NewWithConfig(context.Background(), dbConfig)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}
	defer dbPool.Close()

	// Verify database connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := dbPool.Ping(ctx); err != nil {
		cancel()
		log.Fatalf("Unable to ping database: %v", err)
	}
	cancel()
	slog.Info("database connected")

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
		log.Fatalf("Failed to initialize River client: %v", err)
	}

	// Start River Client
	if err := riverClient.Start(ctx); err != nil {
		log.Fatalf("Failed to start River client: %v", err)
	}

	// Handle OS signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	slog.Info("worker started", "queues", worker.DefaultQueueConfig(cfg))

	// Wait for signal
	sig := <-sigChan
	slog.Info("received shutdown signal", "signal", sig)

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := riverClient.Stop(shutdownCtx); err != nil {
		slog.Error("error stopping River client", "error", err)
	}

	slog.Info("worker stopped")
}
