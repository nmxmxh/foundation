package worker

import (
	"github.com/riverqueue/river"

	"{{MODULE_PATH}}/internal/config"
)

// PeriodicJobs returns the list of periodic jobs to be scheduled.
// Add your periodic job definitions here.
func PeriodicJobs(cfg *config.Config) []*river.PeriodicJob {
	jobs := make([]*river.PeriodicJob, 0)

	// Example periodic job:
	//
	// jobs = append(jobs, river.NewPeriodicJob(
	// 	river.PeriodicInterval(5*time.Minute),
	// 	func() (river.JobArgs, *river.InsertOpts) {
	// 		return ExampleMaintenanceArgs{}, &river.InsertOpts{
	// 			Queue: "scheduled_maintenance",
	// 			UniqueOpts: river.UniqueOpts{
	// 				ByPeriod: 5 * time.Minute,
	// 				ByQueue:  true,
	// 			},
	// 		}
	// 	},
	// 	&river.PeriodicJobOpts{
	// 		ID:         "example_maintenance",
	// 		RunOnStart: true,
	// 	},
	// ))

	return jobs
}
