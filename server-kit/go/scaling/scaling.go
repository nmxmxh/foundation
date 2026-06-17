// Package scaling provides CPU-aware auto-tuning for server resources.
// It automatically configures connection limits, worker pools, and concurrency
// based on available CPU cores, following patterns proven in production.
package scaling

import (
	"runtime"
	"time"
)

// Tier represents a scaling tier based on CPU count.
type Tier int

const (
	TierDevelopment Tier = iota // 1-4 cores
	TierMidRange                // 5-8 cores
	TierProduction              // 9-16 cores
	TierHyperscale              // 16+ cores
)

// Config holds auto-tuned scaling parameters.
type Config struct {
	// Tier is the detected scaling tier.
	Tier Tier

	// CPUCount is the number of available CPU cores.
	CPUCount int

	// WebSocket scaling
	WSMaxConnections   int
	WSWriteQueueDepth  int
	WSReadLimitBytes   int64
	WSPingInterval     time.Duration
	WSGuestIdleTimeout time.Duration
	WSGuestRateLimit   int
	WSGuestRateBurst   int

	// Dispatch concurrency
	DispatchMaxConcurrent  int
	DispatchAcquireTimeout time.Duration

	// Database pool
	DBMaxConnections int
	DBMinConnections int

	// Worker queues
	QueueWorkersDefault        int
	QueueWorkersEventTransport int
	QueueWorkersHeavy          int

	// API rate limiting
	APIRateLimitPerMinute int
	APIRateLimitBurst     int
}

// AutoTune returns a Config tuned for the current system's CPU count.
func AutoTune() Config {
	return AutoTuneForCores(runtime.NumCPU())
}

// AutoTuneForCores returns a Config tuned for a specific core count.
// This is useful for testing or when deploying to known hardware.
func AutoTuneForCores(cores int) Config {
	if cores <= 0 {
		cores = 1
	}

	switch {
	case cores <= 4:
		return developmentConfig(cores)
	case cores <= 8:
		return midRangeConfig(cores)
	case cores <= 16:
		return productionConfig(cores)
	default:
		return hyperscaleConfig(cores)
	}
}

// developmentConfig returns settings for 1-4 core systems.
func developmentConfig(cores int) Config {
	return Config{
		Tier:     TierDevelopment,
		CPUCount: cores,

		WSMaxConnections:   5000,
		WSWriteQueueDepth:  128,
		WSReadLimitBytes:   1 << 20, // 1MB
		WSPingInterval:     30 * time.Second,
		WSGuestIdleTimeout: 60 * time.Second,
		WSGuestRateLimit:   600,
		WSGuestRateBurst:   150,

		DispatchMaxConcurrent:  64,
		DispatchAcquireTimeout: 200 * time.Millisecond,

		DBMaxConnections: 20,
		DBMinConnections: 2,

		QueueWorkersDefault:        8,
		QueueWorkersEventTransport: 4,
		QueueWorkersHeavy:          3,

		APIRateLimitPerMinute: 3000,
		APIRateLimitBurst:     800,
	}
}

// midRangeConfig returns settings for 5-8 core systems.
func midRangeConfig(cores int) Config {
	return Config{
		Tier:     TierMidRange,
		CPUCount: cores,

		WSMaxConnections:   20000,
		WSWriteQueueDepth:  256,
		WSReadLimitBytes:   1 << 20,
		WSPingInterval:     30 * time.Second,
		WSGuestIdleTimeout: 60 * time.Second,
		WSGuestRateLimit:   1200,
		WSGuestRateBurst:   300,

		DispatchMaxConcurrent:  128,
		DispatchAcquireTimeout: 200 * time.Millisecond,

		DBMaxConnections: 40,
		DBMinConnections: 4,

		QueueWorkersDefault:        16,
		QueueWorkersEventTransport: 8,
		QueueWorkersHeavy:          6,

		APIRateLimitPerMinute: 6000,
		APIRateLimitBurst:     1500,
	}
}

// productionConfig returns settings for 9-16 core systems.
func productionConfig(cores int) Config {
	return Config{
		Tier:     TierProduction,
		CPUCount: cores,

		WSMaxConnections:   50000,
		WSWriteQueueDepth:  512,
		WSReadLimitBytes:   1 << 20,
		WSPingInterval:     30 * time.Second,
		WSGuestIdleTimeout: 60 * time.Second,
		WSGuestRateLimit:   2400,
		WSGuestRateBurst:   600,

		DispatchMaxConcurrent:  256,
		DispatchAcquireTimeout: 200 * time.Millisecond,

		DBMaxConnections: 80,
		DBMinConnections: 8,

		QueueWorkersDefault:        32,
		QueueWorkersEventTransport: 12,
		QueueWorkersHeavy:          10,

		APIRateLimitPerMinute: 12000,
		APIRateLimitBurst:     3000,
	}
}

// hyperscaleConfig returns settings for 16+ core systems.
func hyperscaleConfig(cores int) Config {
	return Config{
		Tier:     TierHyperscale,
		CPUCount: cores,

		WSMaxConnections:   100000,
		WSWriteQueueDepth:  1024,
		WSReadLimitBytes:   1 << 20,
		WSPingInterval:     30 * time.Second,
		WSGuestIdleTimeout: 60 * time.Second,
		WSGuestRateLimit:   4000,
		WSGuestRateBurst:   1000,

		DispatchMaxConcurrent:  512,
		DispatchAcquireTimeout: 200 * time.Millisecond,

		DBMaxConnections: 120,
		DBMinConnections: 12,

		QueueWorkersDefault:        48,
		QueueWorkersEventTransport: 16,
		QueueWorkersHeavy:          16,

		APIRateLimitPerMinute: 20000,
		APIRateLimitBurst:     5000,
	}
}

// TierName returns a human-readable name for the tier.
func (t Tier) String() string {
	switch t {
	case TierDevelopment:
		return "development"
	case TierMidRange:
		return "mid-range"
	case TierProduction:
		return "production"
	case TierHyperscale:
		return "hyperscale"
	default:
		return "unknown"
	}
}

// ScaleWorkers returns a worker count scaled by the tier multiplier.
// Useful for domain-specific queue workers that should scale with the system.
func (c Config) ScaleWorkers(base int) int {
	if base <= 0 {
		base = 1
	}

	switch c.Tier {
	case TierDevelopment:
		return base
	case TierMidRange:
		return base * 2
	case TierProduction:
		return base * 4
	case TierHyperscale:
		return base * 6
	default:
		return base
	}
}

// ScaleBuffer returns a buffer size scaled by the tier multiplier.
func (c Config) ScaleBuffer(base int) int {
	if base <= 0 {
		base = 64
	}

	switch c.Tier {
	case TierDevelopment:
		return base
	case TierMidRange:
		return base * 2
	case TierProduction:
		return base * 4
	case TierHyperscale:
		return base * 8
	default:
		return base
	}
}
