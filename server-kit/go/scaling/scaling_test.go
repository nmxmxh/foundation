package scaling

import (
	"testing"
	"time"
)

func TestAutoTuneForCores(t *testing.T) {
	tests := []struct {
		cores        int
		expectedTier Tier
	}{
		{1, TierDevelopment},
		{2, TierDevelopment},
		{4, TierDevelopment},
		{5, TierMidRange},
		{8, TierMidRange},
		{9, TierProduction},
		{16, TierProduction},
		{17, TierHyperscale},
		{32, TierHyperscale},
		{64, TierHyperscale},
	}

	for _, tt := range tests {
		cfg := AutoTuneForCores(tt.cores)
		if cfg.Tier != tt.expectedTier {
			t.Errorf("AutoTuneForCores(%d): got tier %v, want %v", tt.cores, cfg.Tier, tt.expectedTier)
		}
		if cfg.CPUCount != tt.cores {
			t.Errorf("AutoTuneForCores(%d): got CPUCount %d, want %d", tt.cores, cfg.CPUCount, tt.cores)
		}
	}
}

func TestAutoTuneForCoresZeroOrNegative(t *testing.T) {
	cfg := AutoTuneForCores(0)
	if cfg.CPUCount != 1 {
		t.Errorf("AutoTuneForCores(0): got CPUCount %d, want 1", cfg.CPUCount)
	}

	cfg = AutoTuneForCores(-5)
	if cfg.CPUCount != 1 {
		t.Errorf("AutoTuneForCores(-5): got CPUCount %d, want 1", cfg.CPUCount)
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := AutoTuneForCores(4)

	if cfg.WSMaxConnections <= 0 {
		t.Error("WSMaxConnections should be positive")
	}
	if cfg.WSWriteQueueDepth <= 0 {
		t.Error("WSWriteQueueDepth should be positive")
	}
	if cfg.WSReadLimitBytes <= 0 {
		t.Error("WSReadLimitBytes should be positive")
	}
	if cfg.WSPingInterval <= 0 {
		t.Error("WSPingInterval should be positive")
	}
	if cfg.DispatchMaxConcurrent <= 0 {
		t.Error("DispatchMaxConcurrent should be positive")
	}
	if cfg.DBMaxConnections <= 0 {
		t.Error("DBMaxConnections should be positive")
	}
}

func TestTierScaling(t *testing.T) {
	devCfg := AutoTuneForCores(2)
	hyperCfg := AutoTuneForCores(32)

	if hyperCfg.WSMaxConnections <= devCfg.WSMaxConnections {
		t.Error("Hyperscale should have more WS connections than development")
	}
	if hyperCfg.DispatchMaxConcurrent <= devCfg.DispatchMaxConcurrent {
		t.Error("Hyperscale should have higher dispatch concurrency")
	}
	if hyperCfg.DBMaxConnections <= devCfg.DBMaxConnections {
		t.Error("Hyperscale should have more DB connections")
	}
}

func TestScaleWorkers(t *testing.T) {
	devCfg := AutoTuneForCores(2)
	hyperCfg := AutoTuneForCores(32)

	devWorkers := devCfg.ScaleWorkers(4)
	hyperWorkers := hyperCfg.ScaleWorkers(4)

	if hyperWorkers <= devWorkers {
		t.Errorf("ScaleWorkers: hyperscale (%d) should exceed development (%d)", hyperWorkers, devWorkers)
	}
}

func TestScaleBuffer(t *testing.T) {
	devCfg := AutoTuneForCores(2)
	hyperCfg := AutoTuneForCores(32)

	devBuf := devCfg.ScaleBuffer(64)
	hyperBuf := hyperCfg.ScaleBuffer(64)

	if hyperBuf <= devBuf {
		t.Errorf("ScaleBuffer: hyperscale (%d) should exceed development (%d)", hyperBuf, devBuf)
	}
}

func TestTierString(t *testing.T) {
	tests := []struct {
		tier     Tier
		expected string
	}{
		{TierDevelopment, "development"},
		{TierMidRange, "mid-range"},
		{TierProduction, "production"},
		{TierHyperscale, "hyperscale"},
		{Tier(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.tier.String(); got != tt.expected {
			t.Errorf("Tier(%d).String() = %q, want %q", tt.tier, got, tt.expected)
		}
	}
}

func TestConfigTimeouts(t *testing.T) {
	cfg := AutoTuneForCores(8)

	if cfg.WSPingInterval != 30*time.Second {
		t.Errorf("WSPingInterval = %v, want 30s", cfg.WSPingInterval)
	}
	if cfg.WSGuestIdleTimeout != 60*time.Second {
		t.Errorf("WSGuestIdleTimeout = %v, want 60s", cfg.WSGuestIdleTimeout)
	}
	if cfg.DispatchAcquireTimeout != 200*time.Millisecond {
		t.Errorf("DispatchAcquireTimeout = %v, want 200ms", cfg.DispatchAcquireTimeout)
	}
}
