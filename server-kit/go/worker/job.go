package worker

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// Job is the transport shape used by the worker engine.
type Job struct {
	ID              string             `json:"id"`
	JobKind         string             `json:"kind"`
	Queue           string             `json:"queue"`
	Payload         map[string]any     `json:"payload,omitempty"`
	RawPayload      []byte             `json:"raw_payload,omitempty"`
	Metadata        map[string]any     `json:"metadata"`
	CorrelationID   string             `json:"correlation_id"`
	IdempotencyKey  string             `json:"idempotency_key"`
	Attempt         int                `json:"attempt"`
	MaxAttempts     int                `json:"max_attempts"`
	ScheduledAt     time.Time          `json:"scheduled_at,omitzero"`
	CreatedAt       time.Time          `json:"created_at,omitzero"`
	ExecutionPolicy JobExecutionPolicy `json:"execution_policy,omitzero"`
}

// Kind implements river.JobArgs.
func (j Job) Kind() string {
	return j.JobKind
}

type JobExecutionPolicy struct {
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

const DefaultJobTimeoutMS = 30000

func (j *Job) Normalize() {
	if j == nil {
		return
	}
	if strings.TrimSpace(j.Queue) == "" {
		j.Queue = "default"
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 3
	}
	if j.Attempt < 0 {
		j.Attempt = 0
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now().UTC()
	}
	if j.ExecutionPolicy.TimeoutMS <= 0 {
		j.ExecutionPolicy.TimeoutMS = positiveIntFromMetadata(j.Metadata, "timeout_ms", "timeoutMs", "job_timeout_ms", "jobTimeoutMs")
	}
	if j.ExecutionPolicy.TimeoutMS <= 0 {
		j.ExecutionPolicy.TimeoutMS = DefaultJobTimeoutMS
	}
}

func (j Job) Validate() error {
	if strings.TrimSpace(j.JobKind) == "" {
		return errors.New("job.kind is required")
	}
	if strings.TrimSpace(j.Queue) == "" {
		return errors.New("job.queue is required")
	}
	if j.MaxAttempts <= 0 {
		return errors.New("job.max_attempts must be > 0")
	}
	if j.Attempt > j.MaxAttempts {
		return fmt.Errorf("job.attempt %d exceeds max_attempts %d", j.Attempt, j.MaxAttempts)
	}
	return nil
}

func (j Job) NextBackoff() time.Duration {
	base := 250 * time.Millisecond
	max := 30 * time.Second
	backoff := base
	for attempt := 1; attempt < j.Attempt && backoff < max; attempt++ {
		backoff *= 2
		if backoff > max {
			backoff = max
		}
	}
	if backoff > max {
		backoff = max
	}
	// Add ±25% jitter
	jitter := time.Duration(rand.Int63n(int64(backoff/2))) - backoff/4
	return backoff + jitter
}

func (j Job) Timeout() time.Duration {
	timeoutMS := j.ExecutionPolicy.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = DefaultJobTimeoutMS
	}
	return time.Duration(timeoutMS) * time.Millisecond
}

func (j Job) HealthKey() string {
	if trimmed := strings.TrimSpace(j.ID); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(j.CorrelationID); trimmed != "" {
		return "corr:" + trimmed
	}
	if trimmed := strings.TrimSpace(j.IdempotencyKey); trimmed != "" {
		return "idem:" + trimmed
	}
	return fmt.Sprintf("%s:%s:%s", strings.TrimSpace(j.Queue), strings.TrimSpace(j.JobKind), j.CreatedAt.UTC().Format(time.RFC3339Nano))
}

func positiveIntFromMetadata(metadata map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			if typed > 0 {
				return typed
			}
		case int32:
			if typed > 0 {
				return int(typed)
			}
		case int64:
			if typed > 0 {
				return int(typed)
			}
		case float64:
			if typed > 0 {
				return int(typed)
			}
		}
	}
	return 0
}
