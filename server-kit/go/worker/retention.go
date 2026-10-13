package worker

import (
	"time"

	"github.com/riverqueue/river"
)

// RetentionTier represents the operational tier determining job retention duration in River.
type RetentionTier string

const (
	// RetentionTierHuman represents human-scale workflows (e.g. PDF rendering, emails, data export).
	// Retains completed jobs for 24 hours.
	RetentionTierHuman RetentionTier = "human"

	// RetentionTierStandard represents standard background application queues.
	// Retains completed jobs for 4 hours.
	RetentionTierStandard RetentionTier = "standard"

	// RetentionTierHighFrequency represents high-throughput event architectures (e.g. order processing,
	// market data ingestion, Hermes projection updates).
	// Defaults to 2 hours retention to prevent 100k+ completed rows from accumulating in river_job.
	RetentionTierHighFrequency RetentionTier = "high_frequency"

	// DefaultCompletedJobRetentionHuman is the retention duration for human-scale workflows (24h).
	DefaultCompletedJobRetentionHuman = 24 * time.Hour

	// DefaultCompletedJobRetentionStandard is the retention duration for standard queues (4h).
	DefaultCompletedJobRetentionStandard = 4 * time.Hour

	// DefaultCompletedJobRetentionHighFrequency is the retention duration for high-frequency queues (2h).
	DefaultCompletedJobRetentionHighFrequency = 2 * time.Hour

	// DefaultCancelledJobRetentionHighFrequency is the retention duration for cancelled jobs in high-frequency queues (2h).
	DefaultCancelledJobRetentionHighFrequency = 2 * time.Hour

	// DefaultDiscardedJobRetentionHighFrequency is the retention duration for discarded jobs in high-frequency queues (24h).
	DefaultDiscardedJobRetentionHighFrequency = 24 * time.Hour
)

// RetentionPolicy defines job cleaner durations for River.
type RetentionPolicy struct {
	CompletedJobRetention time.Duration
	CancelledJobRetention time.Duration
	DiscardedJobRetention time.Duration
}

// RetentionPolicyForTier returns the recommended retention policy for the specified tier.
func RetentionPolicyForTier(tier RetentionTier) RetentionPolicy {
	switch tier {
	case RetentionTierHuman:
		return RetentionPolicy{
			CompletedJobRetention: DefaultCompletedJobRetentionHuman,
			CancelledJobRetention: DefaultCompletedJobRetentionHuman,
			DiscardedJobRetention: 7 * 24 * time.Hour,
		}
	case RetentionTierStandard:
		return RetentionPolicy{
			CompletedJobRetention: DefaultCompletedJobRetentionStandard,
			CancelledJobRetention: DefaultCompletedJobRetentionStandard,
			DiscardedJobRetention: 48 * time.Hour,
		}
	case RetentionTierHighFrequency:
		fallthrough
	default:
		return RetentionPolicy{
			CompletedJobRetention: DefaultCompletedJobRetentionHighFrequency,
			CancelledJobRetention: DefaultCancelledJobRetentionHighFrequency,
			DiscardedJobRetention: DefaultDiscardedJobRetentionHighFrequency,
		}
	}
}

// ApplyRetentionPolicy sets tier-aware retention defaults on a River Config.
// Any existing non-zero retention duration on cfg is preserved.
func ApplyRetentionPolicy(cfg *river.Config, policy RetentionPolicy) {
	if cfg == nil {
		return
	}
	if cfg.CompletedJobRetentionPeriod == 0 {
		cfg.CompletedJobRetentionPeriod = policy.CompletedJobRetention
	}
	if cfg.CancelledJobRetentionPeriod == 0 {
		cfg.CancelledJobRetentionPeriod = policy.CancelledJobRetention
	}
	if cfg.DiscardedJobRetentionPeriod == 0 {
		cfg.DiscardedJobRetentionPeriod = policy.DiscardedJobRetention
	}
}

// ApplyRetentionDefaults sets tier-aware retention durations on a River client Config.
func (e *Engine) ApplyRetentionDefaults(cfg *river.Config, tier RetentionTier) {
	ApplyRetentionPolicy(cfg, RetentionPolicyForTier(tier))
}
