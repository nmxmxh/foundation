package worker

import (
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
// Adjust worker counts based on your application's needs.
func DefaultQueueConfig(cfg *config.Config) map[string]river.QueueConfig {
	defaultWorkers := 10
	processingWorkers := 4
	scheduledWorkers := 2

	return map[string]river.QueueConfig{
		river.QueueDefault:       {MaxWorkers: defaultWorkers},
		"processing":             {MaxWorkers: processingWorkers},
		"scheduled_maintenance":  {MaxWorkers: scheduledWorkers},
	}
}
