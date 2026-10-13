package worker

import (
	"testing"
	"time"

	"github.com/riverqueue/river"
)

func TestRetentionPolicyForTier(t *testing.T) {
	tests := []struct {
		name              string
		tier              RetentionTier
		expectedCompleted time.Duration
		expectedCancelled time.Duration
		expectedDiscarded time.Duration
	}{
		{
			name:              "human tier",
			tier:              RetentionTierHuman,
			expectedCompleted: 24 * time.Hour,
			expectedCancelled: 24 * time.Hour,
			expectedDiscarded: 7 * 24 * time.Hour,
		},
		{
			name:              "standard tier",
			tier:              RetentionTierStandard,
			expectedCompleted: 4 * time.Hour,
			expectedCancelled: 4 * time.Hour,
			expectedDiscarded: 48 * time.Hour,
		},
		{
			name:              "high_frequency tier",
			tier:              RetentionTierHighFrequency,
			expectedCompleted: 2 * time.Hour,
			expectedCancelled: 2 * time.Hour,
			expectedDiscarded: 24 * time.Hour,
		},
		{
			name:              "unknown tier defaults to high_frequency",
			tier:              RetentionTier("unknown"),
			expectedCompleted: 2 * time.Hour,
			expectedCancelled: 2 * time.Hour,
			expectedDiscarded: 24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := RetentionPolicyForTier(tt.tier)
			if policy.CompletedJobRetention != tt.expectedCompleted {
				t.Errorf("got CompletedJobRetention %v, want %v", policy.CompletedJobRetention, tt.expectedCompleted)
			}
			if policy.CancelledJobRetention != tt.expectedCancelled {
				t.Errorf("got CancelledJobRetention %v, want %v", policy.CancelledJobRetention, tt.expectedCancelled)
			}
			if policy.DiscardedJobRetention != tt.expectedDiscarded {
				t.Errorf("got DiscardedJobRetention %v, want %v", policy.DiscardedJobRetention, tt.expectedDiscarded)
			}
		})
	}
}

func TestApplyRetentionPolicy(t *testing.T) {
	t.Run("nil config is safe", func(t *testing.T) {
		ApplyRetentionPolicy(nil, RetentionPolicyForTier(RetentionTierHighFrequency))
	})

	t.Run("sets default durations when zero", func(t *testing.T) {
		cfg := &river.Config{}
		policy := RetentionPolicyForTier(RetentionTierHighFrequency)
		ApplyRetentionPolicy(cfg, policy)

		if cfg.CompletedJobRetentionPeriod != 2*time.Hour {
			t.Errorf("got CompletedJobRetentionPeriod %v, want 2h", cfg.CompletedJobRetentionPeriod)
		}
		if cfg.CancelledJobRetentionPeriod != 2*time.Hour {
			t.Errorf("got CancelledJobRetentionPeriod %v, want 2h", cfg.CancelledJobRetentionPeriod)
		}
		if cfg.DiscardedJobRetentionPeriod != 24*time.Hour {
			t.Errorf("got DiscardedJobRetentionPeriod %v, want 24h", cfg.DiscardedJobRetentionPeriod)
		}
	})

	t.Run("preserves explicitly configured values", func(t *testing.T) {
		cfg := &river.Config{
			CompletedJobRetentionPeriod: 1 * time.Hour,
			CancelledJobRetentionPeriod: 30 * time.Minute,
			DiscardedJobRetentionPeriod: 12 * time.Hour,
		}
		policy := RetentionPolicyForTier(RetentionTierHuman)
		ApplyRetentionPolicy(cfg, policy)

		if cfg.CompletedJobRetentionPeriod != 1*time.Hour {
			t.Errorf("got CompletedJobRetentionPeriod %v, want 1h", cfg.CompletedJobRetentionPeriod)
		}
		if cfg.CancelledJobRetentionPeriod != 30*time.Minute {
			t.Errorf("got CancelledJobRetentionPeriod %v, want 30m", cfg.CancelledJobRetentionPeriod)
		}
		if cfg.DiscardedJobRetentionPeriod != 12*time.Hour {
			t.Errorf("got DiscardedJobRetentionPeriod %v, want 12h", cfg.DiscardedJobRetentionPeriod)
		}
	})
}

func TestEngineApplyRetentionDefaults(t *testing.T) {
	e := NewEngine(nil, nil)
	cfg := &river.Config{}
	e.ApplyRetentionDefaults(cfg, RetentionTierHighFrequency)

	if cfg.CompletedJobRetentionPeriod != 2*time.Hour {
		t.Errorf("got CompletedJobRetentionPeriod %v, want 2h", cfg.CompletedJobRetentionPeriod)
	}
}
