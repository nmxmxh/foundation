package worker

import "time"

type JobHealthState string

const (
	JobHealthQueued             JobHealthState = "queued"
	JobHealthProcessing         JobHealthState = "processing"
	JobHealthRetryScheduled     JobHealthState = "retry_scheduled"
	JobHealthSucceeded          JobHealthState = "succeeded"
	JobHealthTimedOut           JobHealthState = "timed_out"
	JobHealthFailedExhausted    JobHealthState = "failed_exhausted"
	JobHealthDroppedNoProcessor JobHealthState = "dropped_no_processor"
	JobHealthRejectedQueueFull  JobHealthState = "rejected_queue_full"
	JobHealthDeduped            JobHealthState = "deduped"
)

type JobHealthSnapshot struct {
	Key           string         `json:"key"`
	Kind          string         `json:"kind"`
	Queue         string         `json:"queue"`
	State         JobHealthState `json:"state"`
	Attempt       int            `json:"attempt"`
	MaxAttempts   int            `json:"max_attempts"`
	TimeoutMS     int            `json:"timeout_ms"`
	CorrelationID string         `json:"correlation_id,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	ScheduledAt   time.Time      `json:"scheduled_at,omitempty"`
	StartedAt     time.Time      `json:"started_at,omitempty"`
	FinishedAt    time.Time      `json:"finished_at,omitempty"`
	UpdatedAt     time.Time      `json:"updated_at"`
}
