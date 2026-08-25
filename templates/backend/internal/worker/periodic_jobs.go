// Package worker registers bounded background jobs for the application.
package worker

import (
	"github.com/riverqueue/river"

	"{{MODULE_PATH}}/internal/config"
)

// PeriodicJobs returns the list of periodic jobs to be scheduled.
// Add your periodic job definitions here.
//
// Two rules govern a recurring producer, and both are load-bearing.
//
// A durable queue is a crash-safety mechanism, not a scheduler clock. A sweep
// that submits every candidate on every tick and leans on uniqueness to reject
// the duplicates pays for every rejection in the database. Keep due-times in
// cheap local state and submit only due work, so most sweeps submit nothing.
//
// When uniqueness is the safety net rather than the clock, scope it with an
// explicit ByState. rivertype.UniqueOptsByStateDefault includes `completed`, so
// default-scoped uniqueness stops recurrence after the first job finishes:
// ingestion dies with no error and no log line. Recurring args must exclude the
// terminal states `completed`, `cancelled`, and `discarded`.
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
	// 				// Non-terminal states only. Without this, one completed
	// 				// job blocks every future insert for the same key until
	// 				// it is reaped, and the schedule silently stops.
	// 				ByState: []rivertype.JobState{
	// 					rivertype.JobStateAvailable,
	// 					rivertype.JobStatePending,
	// 					rivertype.JobStateRetryable,
	// 					rivertype.JobStateRunning,
	// 					rivertype.JobStateScheduled,
	// 				},
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
