// Package worker registers bounded background jobs for the application.
package worker

import (
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
	workerkit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
	"github.com/riverqueue/river"

	"{{MODULE_PATH}}/internal/config"
)

// Dependencies holds all services required by workers.
// Add your domain services here as needed.
type Dependencies struct {
	DB     *pgxpool.Pool
	Config *config.Config
	// Projected + ProjectionFetch arm the canonical hermes record-projection
	// processor: repos enqueue hermes.NewRecordProjectionJob in their command
	// transactions, and the processor resolves committed rows through
	// ProjectionFetch (read-back from your normalized tables) into the
	// projected store. Leave nil until the app wires its fetcher.
	Projected       *hermes.ProjectedRuntimeStore
	ProjectionFetch hermes.RecordFetcher
	// Add domain services here, e.g.:
	// UserService *user.Service
}

// RegisterAll registers all workers with the given river.Workers bundle.
// Add worker registrations here as you implement them.
func RegisterAll(workers *river.Workers, deps *Dependencies) {
	if deps == nil || deps.Config == nil {
		return
	}

	// Example worker registration:
	// river.AddWorker(workers, &ExampleWorker{Service: deps.ExampleService})
}

// RegisterProcessors registers foundation worker.Processor implementations on
// the engine; they bridge onto the river bundle via engine.AddToWorkers. This
// is the canonical seam for foundation-shaped jobs — raw river.Worker
// registrations in RegisterAll coexist on the same client.
func RegisterProcessors(engine *workerkit.Engine, deps *Dependencies) {
	if engine == nil || deps == nil {
		return
	}
	if deps.Projected != nil && deps.ProjectionFetch != nil {
		if processor, err := hermes.NewRecordProjectionProcessor(deps.Projected, deps.ProjectionFetch); err == nil {
			_ = engine.Register(processor)
		}
	}
}

// DefaultQueueConfig returns queue configuration for River.
// Queue limits are environment-driven to keep runtime concurrency tunable.
func DefaultQueueConfig(cfg *config.Config) map[string]river.QueueConfig {
	_ = cfg

	return map[string]river.QueueConfig{
		river.QueueDefault:           {MaxWorkers: envInt("QUEUE_WORKERS_DEFAULT", 10)},
		"processing":                 {MaxWorkers: envInt("QUEUE_WORKERS_PROCESSING", 4)},
		"scheduled_maintenance":      {MaxWorkers: envInt("QUEUE_WORKERS_SCHEDULED", 2)},
		hermes.RecordProjectionQueue: {MaxWorkers: envInt("QUEUE_WORKERS_PROJECTION", 4)},
	}
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
