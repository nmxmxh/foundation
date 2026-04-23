package startup

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/lib/pq"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/healthcheck"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/resilience"
	"github.com/redis/go-redis/v9"

	"{{MODULE_PATH}}/internal/config"
)

// Dependencies holds all initialized dependencies
type Dependencies struct {
	DB            *sql.DB
	Redis         *redis.Client
	HealthChecker *healthcheck.HealthChecker
	Resilience    *resilience.Runtime
}

// InitDependencies initializes all application dependencies
func InitDependencies(ctx context.Context, cfg *config.Config) (*Dependencies, func(), error) {
	deps := &Dependencies{}
	var cleanups []func()

	// Initialize database
	db, err := initDatabase(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("init database: %w", err)
	}
	deps.DB = db
	cleanups = append(cleanups, func() {
		if err := db.Close(); err != nil {
			slog.Error("failed to close database", "error", err)
		}
	})

	// Initialize Redis (optional)
	if cfg.RedisURL != "" {
		redisClient, err := initRedis(ctx, cfg.RedisURL)
		if err != nil {
			slog.Warn("failed to initialize redis, continuing without it", "error", err)
		} else {
			deps.Redis = redisClient
			cleanups = append(cleanups, func() {
				if err := redisClient.Close(); err != nil {
					slog.Error("failed to close redis", "error", err)
				}
			})
		}
	}

	// Initialize health checker
	deps.HealthChecker = initHealthChecker(deps.DB, deps.Redis)

	// Initialize resilience patterns
	resilienceRuntime, err := initResilience(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("init resilience: %w", err)
	}
	deps.Resilience = resilienceRuntime
	cleanups = append(cleanups, func() {
		if err := resilienceRuntime.Close(context.Background()); err != nil {
			slog.Error("failed to close resilience runtime", "error", err)
		}
	})

	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	return deps, cleanup, nil
}

func initDatabase(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(1 * time.Minute)

	// Verify connection
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	slog.Info("database connected")
	return db, nil
}

func initRedis(ctx context.Context, url string) (*redis.Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	slog.Info("redis connected")
	return client, nil
}

func initHealthChecker(db *sql.DB, redisClient *redis.Client) *healthcheck.HealthChecker {
	hc := healthcheck.New(healthcheck.Config{
		ServiceName:    "{{MODULE_PATH}}",
		ServiceVersion: "1.0.0",
		DefaultTimeout: 5 * time.Second,
	})

	// Add database check
	hc.AddCheck("database", healthcheck.DatabaseCheck(db))

	// Add redis check if available
	if redisClient != nil {
		hc.AddCheck("redis", healthcheck.CustomCheck("redis", func(ctx context.Context) error {
			return redisClient.Ping(ctx).Err()
		}))
	}

	return hc
}

func initResilience(ctx context.Context) (*resilience.Runtime, error) {
	cfg := resilience.DefaultConfig("{{MODULE_PATH}}")
	cfg.CircuitBreakerFailureThreshold = 5
	cfg.CircuitBreakerSuccessThreshold = 2
	cfg.CircuitBreakerTimeout = 30 * time.Second
	cfg.RetryMaxAttempts = 3
	cfg.RetryInitialDelay = 100 * time.Millisecond
	cfg.RetryMaxDelay = 2 * time.Second

	return resilience.New(ctx, cfg)
}
