package worker

import (
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"{{MODULE_PATH}}/internal/config"
)

// Dependencies holds all services required by workers.
// Add your domain services here as needed.
type Dependencies struct {
	DB     *pgxpool.Pool
	Config *config.Config
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

// DefaultQueueConfig returns queue configuration for River.
// Queue limits are environment-driven to keep runtime concurrency tunable.
func DefaultQueueConfig(cfg *config.Config) map[string]river.QueueConfig {
	_ = cfg

	return map[string]river.QueueConfig{
		river.QueueDefault:      {MaxWorkers: envInt("QUEUE_WORKERS_DEFAULT", 10)},
		"processing":            {MaxWorkers: envInt("QUEUE_WORKERS_PROCESSING", 4)},
		"scheduled_maintenance": {MaxWorkers: envInt("QUEUE_WORKERS_SCHEDULED", 2)},
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
