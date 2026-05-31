// Package startup initializes infrastructure dependencies for the application.
package startup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/healthcheck"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	kitlogger "github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
	rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/resilience"

	"{{MODULE_PATH}}/internal/config"
)

// Dependencies holds all initialized dependencies
type Dependencies struct {
	DB            database.RuntimeStore
	Hermes        *hermes.Store
	Redis         rediskit.Client
	Bus           events.Bus
	closeBus      func() error
	HealthChecker *healthcheck.HealthChecker
	Resilience    *resilience.Runtime
	Handler       *graceful.Handler
	Registry      *registry.ServiceRegistry
	FrameRouter   *grpcsvc.Router
	Log           kitlogger.Logger
}

// InitDependencies initializes all application dependencies
func InitDependencies(ctx context.Context, cfg *config.Config) (*Dependencies, func(), error) {
	deps := &Dependencies{}
	var cleanups []func(context.Context)
	kitLog := kitlogger.Default().With("component", "startup")
	deps.Log = kitLog

	db, hermesStore, err := initDatabase(ctx, cfg, kitLog)
	if err != nil {
		return nil, nil, fmt.Errorf("init database: %w", err)
	}
	deps.DB = db
	deps.Hermes = hermesStore
	cleanups = append(cleanups, func(context.Context) {
		db.Close()
	})

	redisClient, bus, closeBus, err := initEventBus(ctx, cfg, kitLog)
	if err != nil {
		if cfg.IsProduction() {
			return nil, nil, fmt.Errorf("init event bus: %w", err)
		}
		kitLog.WarnContext(ctx, "failed to initialize redis event bus, using in-memory bus", "error", err)
		bus = events.NewInMemoryBus(200)
	}
	deps.Redis = redisClient
	deps.Bus = bus
	deps.closeBus = closeBus
	if closeBus != nil {
		cleanups = append(cleanups, func(cleanupCtx context.Context) {
			if err := closeBus(); err != nil {
				kitLog.ErrorContext(cleanupCtx, "failed to close event bus", "error", err)
			}
		})
	}
	if redisClient != nil {
		cleanups = append(cleanups, func(cleanupCtx context.Context) {
			if err := redisClient.Close(); err != nil {
				kitLog.ErrorContext(cleanupCtx, "failed to close redis", "error", err)
			}
		})
	}

	deps.HealthChecker = initHealthChecker(deps.DB, deps.Redis)

	deps.Handler = graceful.NewHandler(
		graceful.WithLogger(kitLog),
		graceful.WithService("{{PROJECT_NAME}}"),
		graceful.WithVersion("1.0.0"),
		graceful.WithEventEmitter(graceful.NewRedisEventEmitter(deps.Bus)),
	)
	deps.Registry = registry.New(deps.Redis, deps.Handler, kitLog)
	deps.FrameRouter = grpcsvc.NewRouter()

	resilienceRuntime, err := initResilience(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("init resilience: %w", err)
	}
	deps.Resilience = resilienceRuntime
	cleanups = append(cleanups, func(ctx context.Context) {
		if err := resilienceRuntime.Close(ctx); err != nil {
			kitLog.ErrorContext(ctx, "failed to close resilience runtime", "error", err)
		}
	})
	bindResilienceDependencies(deps)

	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i](cleanupCtx)
		}
	}

	return deps, cleanup, nil
}

func initDatabase(ctx context.Context, cfg *config.Config, log kitlogger.Logger) (database.RuntimeStore, *hermes.Store, error) {
	db, err := database.Connect(ctx, cfg.DatabaseURL, cfg.StateStore, database.PoolOptions{
		MaxConns:          cfg.DBMaxConns,
		MinConns:          cfg.DBMinConns,
		HealthCheckPeriod: cfg.DBHealthCheckPeriod,
		ConnectTimeout:    cfg.DBConnectTimeout,
		QueryTimeout:      cfg.DBQueryTimeout,
		AcquireTimeout:    cfg.DBAcquireTimeout,
	})
	if err != nil {
		return nil, nil, err
	}
	projected, err := hermes.WrapRuntimeStore(db, hermes.RuntimeStoreOptions{
		IndexedFields:      cfg.HermesIndexedFields,
		MaxRecordsPerScope: cfg.HermesMaxRecords,
		MaxBytesPerScope:   cfg.HermesMaxBytes,
	})
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	log.InfoContext(ctx, "database connected", "driver", cfg.StateStore, "hermes", "enabled")
	return projected, projected.Store(), nil
}

func initEventBus(ctx context.Context, cfg *config.Config, log kitlogger.Logger) (rediskit.Client, events.Bus, func() error, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.EventBus))
	if driver == "" {
		driver = rediskit.DriverRedis
	}
	if driver != rediskit.DriverRedis {
		bus := events.NewInMemoryBus(200)
		return nil, bus, nil, nil
	}

	redisClient, err := rediskit.ConnectWithOptions(rediskit.Options{
		URL:          cfg.RedisURL,
		URLs:         splitCSV(cfg.RedisShardURLs),
		Prefix:       cfg.RedisPrefix,
		Driver:       rediskit.DriverRedis,
		PoolSize:     cfg.RedisPoolSize,
		MinIdle:      cfg.RedisMinIdle,
		MaxRetries:   cfg.RedisMaxRetries,
		DialTimeout:  cfg.RedisDialTimeout,
		ReadTimeout:  cfg.RedisReadTimeout,
		WriteTimeout: cfg.RedisWriteTimeout,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	bus := events.NewRedisBus(redisClient, "events", 200, log)
	log.InfoContext(ctx, "redis event bus connected", "prefix", cfg.RedisPrefix)
	return redisClient, bus, bus.Close, nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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
			if _, err := redisClient.Expire(ctx, "__health_check__", time.Minute); err != nil {
				return err
			}
			return nil
		}))
	}
	if projected, ok := db.(interface{ HermesHealth(context.Context) error }); ok {
		hc.AddCheck("hermes", healthcheck.CustomCheck("hermes", projected.HermesHealth))
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
				if _, err := deps.Redis.Expire(ctx, "__resilience_health_check__", time.Minute); err != nil {
					return err
				}
				return nil
			},
			resilience.WithCritical(false),
			resilience.WithFailureThreshold(3),
			resilience.WithFallbackBehavior("fail_open"),
		)
	}
	if deps.DB != nil {
		deps.Resilience.RegisterDependency(
			"hermes",
			func(ctx context.Context) error {
				projected, ok := deps.DB.(interface{ HermesHealth(context.Context) error })
				if !ok {
					return fmt.Errorf("hermes projected runtime store is not registered")
				}
				return projected.HermesHealth(ctx)
			},
			resilience.WithCritical(false),
			resilience.WithFailureThreshold(3),
			resilience.WithFallbackBehavior("postgres_fallback"),
		)
	}
}
