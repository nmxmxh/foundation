package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	workerkit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
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

	// River gets a dedicated pool, isolated from the domain pool above. The
	// domain pool stamps a session statement_timeout (the hot-path query budget)
	// on every connection; on River's pool that budget would cancel legitimate
	// batch inserts (JobInsertFastMany) and the maintenance sweeps, surfacing as
	// "canceling statement due to statement timeout". River bounds its own
	// queries with context deadlines, so this pool omits that guardrail.
	// RIVER_DIRECT_URL additionally bypasses PgBouncer transaction pooling so
	// LISTEN/NOTIFY wakes work; empty falls back to DATABASE_URL.
	riverDSN := cfg.DatabaseURL
	if strings.TrimSpace(cfg.RiverDirectURL) != "" {
		riverDSN = cfg.RiverDirectURL
	}
	riverPool, err := database.NewRiverPool(context.Background(), riverDSN, workerPoolOptions(cfg))
	if err != nil {
		log.Error("unable to connect river database pool", "error", err)
		os.Exit(1)
	}
	defer riverPool.Close()

	// Verify database connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if pingErr := dbPool.Ping(ctx); pingErr != nil {
		cancel()
		log.ErrorContext(ctx, "unable to ping database", "error", pingErr)
		os.Exit(1)
	}
	cancel()
	log.Info("database connected")

	// Resynchronize river_job_id_seq sequence in case explicit IDs were inserted (e.g. during load tests or seed scripts)
	if resyncErr := database.ResyncRiverJobSequence(context.Background(), riverPool); resyncErr != nil {
		log.Warn("unable to resynchronize river_job sequence", "error", resyncErr)
	}

	// Create context that cancels on interrupt signal

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	// Initialize River Workers
	workers := river.NewWorkers()
	deps := &worker.Dependencies{
		DB:     dbPool,
		Config: cfg,
		// Add your initialized services here
	}
	worker.RegisterAll(workers, deps)

	// Foundation worker engine: the canonical seam for Processor-based jobs
	// (e.g. the hermes record-projection processor). Engine processors bridge
	// onto the same river bundle as the raw river.Worker registrations above,
	// and the engine is the EnqueueTx/Enqueue surface for foundation jobs.
	engine := workerkit.NewEngine(nil, log)
	if err := worker.RegisterProcessors(engine, deps); err != nil {
		log.ErrorContext(ctx, "failed to register engine processors", "error", err)
		os.Exit(1)
	}
	if err := engine.AddToWorkers(workers); err != nil {
		log.ErrorContext(ctx, "failed to bridge engine processors onto river", "error", err)
		os.Exit(1)
	}

	// Initialize River Client
	riverClient, err := river.NewClient(riverpgxv5.New(riverPool), &river.Config{
		Workers:      workers,
		Queues:       engine.RiverQueueConfig(worker.DefaultQueueConfig(cfg)),
		PeriodicJobs: worker.PeriodicJobs(cfg),
	})
	if err != nil {
		log.Error("failed to initialize River client", "error", err)
		os.Exit(1)
	}
	engine.SetRiverClient(riverClient, dbPool)

	// Wait for River tables to be ready before starting queue polling
	if err := database.WaitForRiverTableReady(ctx, riverPool, 30*time.Second); err != nil {
		log.ErrorContext(ctx, "river_job table not ready", "error", err)
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
