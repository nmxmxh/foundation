// Package startup initializes infrastructure dependencies for the application.
package startup

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/graceful"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/healthcheck"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermessnapshot"
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
	Projected     *hermes.ProjectedRuntimeStore
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
	// The projected runtime store is what the projection gateway resolves scopes
	// against (it owns the partition naming). db is always the projected store.
	if projected, ok := db.(*hermes.ProjectedRuntimeStore); ok {
		deps.Projected = projected
		warmProjectionScopes(ctx, projected, cfg.HermesWarmScopes, kitLog)
	}
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
		// closeBus wraps RedisBus.Close(), which cancels the listener ctx and
		// closes the shared redis client. Registering a separate redisClient.Close()
		// cleanup would double-close: cleanups run LIFO, so the client would close
		// first, then the listener goroutine's in-flight Subscribe would log
		// "redis: client is closed", followed by RedisBus.Close() failing on the
		// same already-closed client. One cleanup, one owner.
		cleanups = append(cleanups, func(cleanupCtx context.Context) {
			if err := closeBus(); err != nil {
				kitLog.ErrorContext(cleanupCtx, "failed to close event bus", "error", err)
			}
		})
	}

	// Fallback projection population: tail canonical projection envelopes from
	// Redis Streams for each warm scope. The canonical path is a
	// hermes.RecordWorkerProcessor on the River queue (durable, tx-coupled);
	// this tailer covers producers that cannot share the Postgres job queue.
	if cfg.HermesEnvelopeFallback && deps.Projected != nil && deps.Redis != nil {
		startProjectionEnvelopeFallback(ctx, deps.Projected, deps.Redis, cfg.HermesWarmScopes, kitLog)
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

// warmProjectionScopes eagerly rebuilds the hermes hot partitions for the
// configured scopes so the projection gateway serves out-of-band (e.g.
// SQL-seeded) rows instead of "projection not found". Each scope is
// "domain:collection:organization". Warming failures are logged, not fatal:
// the projected store falls back to the database on read, and a warm scope is
// re-attempted lazily on the first read-through.
func warmProjectionScopes(ctx context.Context, projected *hermes.ProjectedRuntimeStore, scopes []string, log kitlogger.Logger) {
	for _, scope := range scopes {
		parts := strings.Split(scope, ":")
		if len(parts) != 3 {
			log.WarnContext(ctx, "skipping malformed hermes warm scope; want domain:collection:organization", "scope", scope)
			continue
		}
		domain, collection, organization := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		if domain == "" || collection == "" || organization == "" {
			log.WarnContext(ctx, "skipping hermes warm scope with empty component", "scope", scope)
			continue
		}
		if err := projected.WarmScope(ctx, domain, collection, organization); err != nil {
			log.WarnContext(ctx, "failed to warm hermes projection scope; will warm lazily on first read",
				"domain", domain, "collection", collection, "organization", organization, "error", err)
			continue
		}
		log.InfoContext(ctx, "warmed hermes projection scope",
			"domain", domain, "collection", collection, "organization", organization)
	}
}

// startProjectionEnvelopeFallback runs one hardened hermes.EnvelopeTailer per
// warm scope, consuming canonical projection envelopes
// (foundation.v1.RecordMutationBatch) from the Redis stream
// hermes:projection:<domain>:<collection>:<organization>. Poison envelopes are
// quarantined by the tailer, so only system errors (e.g. Redis down) surface
// here; each tailer restarts with a fixed backoff until ctx ends. WarmScope
// runs first so the partition is registered and seeded before deltas apply.
func startProjectionEnvelopeFallback(ctx context.Context, projected *hermes.ProjectedRuntimeStore, client rediskit.Client, scopes []string, log kitlogger.Logger) {
	consumer, err := os.Hostname()
	if err != nil || strings.TrimSpace(consumer) == "" {
		consumer = "envelope_fallback"
	}
	for _, scope := range scopes {
		parts := strings.Split(scope, ":")
		if len(parts) != 3 {
			continue // warmProjectionScopes already logged the malformed scope
		}
		domain, collection, organization := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		if domain == "" || collection == "" || organization == "" {
			continue
		}
		if err := projected.WarmScope(ctx, domain, collection, organization); err != nil {
			log.WarnContext(ctx, "envelope fallback: warm before tail failed; skipping scope",
				"domain", domain, "collection", collection, "organization", organization, "error", err)
			continue
		}
		stream := "hermes:projection:" + domain + ":" + collection + ":" + organization
		source, err := hermes.NewRedisStreamEnvelopeSource(client, stream, "hermes_projection", consumer, "")
		if err != nil {
			log.WarnContext(ctx, "envelope fallback: source init failed", "stream", stream, "error", err)
			continue
		}
		projection := projected.ProjectionName(domain, collection, organization)
		tailer, err := hermes.NewEnvelopeTailer(projected.Store(), projection, source, hermes.TailerOptions{})
		if err != nil {
			log.WarnContext(ctx, "envelope fallback: tailer init failed", "projection", projection, "error", err)
			continue
		}
		log.InfoContext(ctx, "envelope fallback: tailing projection envelopes", "stream", stream, "projection", projection)
		go func() {
			for {
				err := tailer.Run(ctx)
				if ctx.Err() != nil {
					return
				}
				log.WarnContext(ctx, "envelope fallback: tailer stopped; restarting", "stream", stream, "error", err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}()
	}
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
	storeOpts := hermes.RuntimeStoreOptions{
		IndexedFields:      cfg.HermesIndexedFields,
		MaxRecordsPerScope: cfg.HermesMaxRecords,
		MaxBytesPerScope:   cfg.HermesMaxBytes,
	}
	// Shadow-mode snapshot rollout: with a snapshot directory configured, every
	// source rebuild diffs and refreshes a durable artifact (evidence counters
	// in hermes runtime stats). The served warm path is unchanged.
	if dir := strings.TrimSpace(cfg.HermesSnapshotDir); dir != "" {
		snaps, err := hermessnapshot.NewFileStore(dir)
		if err != nil {
			db.Close()
			return nil, nil, fmt.Errorf("init hermes snapshot store: %w", err)
		}
		storeOpts.SnapshotStore = snaps
	}
	projected, err := hermes.WrapRuntimeStore(db, storeOpts)
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
