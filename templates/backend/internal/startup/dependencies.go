package startup

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/healthcheck"
	kitlogger "github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/resilience"

	"{{MODULE_PATH}}/internal/config"
)

// Dependencies holds all initialized dependencies
type Dependencies struct {
	DB            database.RuntimeStore
	Redis         rediskit.Client
	Bus           events.Bus
	closeBus      func() error
	HealthChecker *healthcheck.HealthChecker
	Resilience    *resilience.Runtime
	Handler       *graceful.Handler
	Registry      *registry.ServiceRegistry
}

// InitDependencies initializes all application dependencies
func InitDependencies(ctx context.Context, cfg *config.Config) (*Dependencies, func(), error) {
	deps := &Dependencies{}
	var cleanups []func()

	db, err := initDatabase(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("init database: %w", err)
	}
	deps.DB = db
	cleanups = append(cleanups, func() {
		db.Close()
	})

	redisClient, bus, closeBus, err := initEventBus(cfg)
	if err != nil {
		if cfg.IsProduction() {
			return nil, nil, fmt.Errorf("init event bus: %w", err)
		}
		slog.Warn("failed to initialize redis event bus, using in-memory bus", "error", err)
		bus = events.NewInMemoryBus(200)
	}
	deps.Redis = redisClient
	deps.Bus = bus
	deps.closeBus = closeBus
	if closeBus != nil {
		cleanups = append(cleanups, func() {
			if err := closeBus(); err != nil {
				slog.Error("failed to close event bus", "error", err)
			}
		})
	}
	if redisClient != nil {
		cleanups = append(cleanups, func() {
			if err := redisClient.Close(); err != nil {
				slog.Error("failed to close redis", "error", err)
			}
		})
	}

	deps.HealthChecker = initHealthChecker(deps.DB, deps.Redis)

	kitLog, err := kitlogger.NewDefault()
	if err != nil {
		return nil, nil, fmt.Errorf("init foundation logger: %w", err)
	}
	deps.Handler = graceful.NewHandler(
		graceful.WithLogger(kitLog),
		graceful.WithService("{{PROJECT_NAME}}"),
		graceful.WithVersion("1.0.0"),
		graceful.WithEventEmitter(graceful.NewRedisEventEmitter(deps.Bus)),
	)
	deps.Registry = registry.New(deps.Redis, deps.Handler, kitLog)

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
	bindResilienceDependencies(deps)

	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	return deps, cleanup, nil
}

func initDatabase(ctx context.Context, cfg *config.Config) (database.RuntimeStore, error) {
	db, err := database.Connect(ctx, cfg.DatabaseURL, cfg.StateStore, database.PoolOptions{
		MaxConns:          25,
		MinConns:          2,
		HealthCheckPeriod: 30 * time.Second,
		ConnectTimeout:    5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	slog.Info("database connected", "driver", cfg.StateStore)
	return db, nil
}

func initEventBus(cfg *config.Config) (rediskit.Client, events.Bus, func() error, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.EventBus))
	if driver == "" {
		driver = rediskit.DriverRedis
	}
	if driver != rediskit.DriverRedis {
		bus := events.NewInMemoryBus(200)
		return nil, bus, nil, nil
	}

	redisClient, err := rediskit.Connect(cfg.RedisURL, cfg.RedisPrefix, rediskit.DriverRedis)
	if err != nil {
		return nil, nil, nil, err
	}
	bus := events.NewRedisBus(redisClient, "events", 200, nil)
	slog.Info("redis event bus connected", "prefix", cfg.RedisPrefix)
	return redisClient, bus, bus.Close, nil
}

func initHealthChecker(db database.RuntimeStore, redisClient rediskit.Client) *healthcheck.HealthChecker {
	hc := healthcheck.New(healthcheck.Config{
		ServiceName:    "{{MODULE_PATH}}",
		ServiceVersion: "1.0.0",
		DefaultTimeout: 5 * time.Second,
	})

	hc.AddCheck("database", healthcheck.CustomCheck("database", func(ctx context.Context) error {
		return db.Exec(ctx, "SELECT 1")
	}))

	if redisClient != nil {
		hc.AddCheck("redis", healthcheck.CustomCheck("redis", func(ctx context.Context) error {
			_, err := redisClient.Incr(ctx, "__health_check__")
			if err != nil {
				return err
			}
			_, _ = redisClient.Expire(ctx, "__health_check__", time.Minute)
			return nil
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

func bindResilienceDependencies(deps *Dependencies) {
	if deps == nil || deps.Resilience == nil {
		return
	}
	if deps.DB != nil {
		deps.Resilience.RegisterDependency(
			"database",
			func(ctx context.Context) error {
				return deps.DB.Exec(ctx, "SELECT 1")
			},
			resilience.WithCritical(true),
			resilience.WithFailureThreshold(3),
		)
	}
	if deps.Redis != nil {
		deps.Resilience.RegisterDependency(
			"redis",
			func(ctx context.Context) error {
				_, err := deps.Redis.Incr(ctx, "__resilience_health_check__")
				if err != nil {
					return err
				}
				_, _ = deps.Redis.Expire(ctx, "__resilience_health_check__", time.Minute)
				return nil
			},
			resilience.WithCritical(false),
			resilience.WithFailureThreshold(3),
			resilience.WithFallbackBehavior("fail_open"),
		)
	}
}
