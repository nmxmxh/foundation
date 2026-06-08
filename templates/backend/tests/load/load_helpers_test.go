package load

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMaxStep(t *testing.T) {
	if got := maxStep([]int{5, 10, 3, 11, 2}); got != 11 {
		t.Fatalf("maxStep = %d, want 11", got)
	}
	if got := maxStep(nil); got != 0 {
		t.Fatalf("maxStep(nil) = %d, want 0", got)
	}
}

func TestLoadThinkTimeFromEnv(t *testing.T) {
	withEnv(t, "LOAD_THINK_TIME_MS", "25")
	if got := loadThinkTimeFromEnv(10 * time.Millisecond); got != 25*time.Millisecond {
		t.Fatalf("loadThinkTimeFromEnv = %s, want 25ms", got)
	}
	withEnv(t, "LOAD_THINK_TIME_MS", "-1")
	if got := loadThinkTimeFromEnv(10 * time.Millisecond); got != 10*time.Millisecond {
		t.Fatalf("invalid env fallback = %s, want 10ms", got)
	}
}

func TestLoadOpTimeoutFromEnv(t *testing.T) {
	withEnv(t, "LOAD_OP_TIMEOUT_MS", "900")
	if got := loadOpTimeoutFromEnv(2 * time.Second); got != 900*time.Millisecond {
		t.Fatalf("loadOpTimeoutFromEnv = %s, want 900ms", got)
	}
	withEnv(t, "LOAD_OP_TIMEOUT_MS", "0")
	if got := loadOpTimeoutFromEnv(2 * time.Second); got != 2*time.Second {
		t.Fatalf("invalid env fallback = %s, want 2s", got)
	}
}

func TestClampStepsToCap(t *testing.T) {
	capped := clampStepsToCap([]int{50, 100, 200, 400}, 180)
	if len(capped) != 2 || capped[0] != 50 || capped[1] != 100 {
		t.Fatalf("unexpected capped steps: %#v", capped)
	}

	fallback := clampStepsToCap([]int{300, 400}, 200)
	if len(fallback) != 1 || fallback[0] != 200 {
		t.Fatalf("unexpected fallback steps: %#v", fallback)
	}
}

func TestInferLoadProfileForCores(t *testing.T) {
	pSmall := inferLoadProfileForCores(2)
	if pSmall.Class != "small" || pSmall.Steps[0] != 8 {
		t.Fatalf("unexpected small profile: %#v", pSmall)
	}

	pMedium := inferLoadProfileForCores(8)
	if pMedium.Class != "medium" || pMedium.Steps[len(pMedium.Steps)-1] != 64 {
		t.Fatalf("unexpected medium profile: %#v", pMedium)
	}

	pLarge := inferLoadProfileForCores(16)
	if pLarge.Class != "large" || pLarge.Steps[len(pLarge.Steps)-1] != 128 {
		t.Fatalf("unexpected large profile: %#v", pLarge)
	}
}

func TestLoadLatencyHistogramSummary(t *testing.T) {
	var histogram loadLatencyHistogram
	for _, micros := range []int64{100, 500, 900, 1_200, 1_900, 9_000, 11_000, 21_000, 101_000, 2_400_000} {
		histogram.record(micros)
	}

	summary := histogram.summary()
	if summary.Count != 10 {
		t.Fatalf("summary.Count = %d, want 10", summary.Count)
	}
	if summary.P50 != 2_000 {
		t.Fatalf("summary.P50 = %d, want 2000", summary.P50)
	}
	if summary.P95 != 3_000_000 {
		t.Fatalf("summary.P95 = %d, want 3000000", summary.P95)
	}
	if summary.P99 != 3_000_000 {
		t.Fatalf("summary.P99 = %d, want 3000000", summary.P99)
	}
	if summary.Max != 2_400_000 {
		t.Fatalf("summary.Max = %d, want 2400000", summary.Max)
	}
}

func withEnv(t *testing.T, key, value string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("set env %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			if err := os.Setenv(key, prev); err != nil {
				t.Fatalf("restore env %s: %v", key, err)
			}
		} else {
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("unset env %s: %v", key, err)
			}
		}
	})
}

func loadStepsFromEnv(defaultSteps []int) []int {
	raw := strings.TrimSpace(os.Getenv("LOAD_STEPS"))
	if raw == "" {
		return defaultSteps
	}
	parts := strings.Split(raw, ",")
	steps := make([]int, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			continue
		}
		steps = append(steps, n)
	}
	if len(steps) == 0 {
		return defaultSteps
	}
	return steps
}

func loadStepDurationFromEnv(defaultDuration time.Duration) time.Duration {
	return durationFromEnvSeconds("LOAD_STEP_DURATION_SEC", defaultDuration)
}

func loadThinkTimeFromEnv(defaultDuration time.Duration) time.Duration {
	return durationFromEnvMillis("LOAD_THINK_TIME_MS", defaultDuration, true)
}

func loadOpTimeoutFromEnv(defaultDuration time.Duration) time.Duration {
	return durationFromEnvMillis("LOAD_OP_TIMEOUT_MS", defaultDuration, false)
}

func loadMaxConcurrencyCapFromEnv(defaultCap int) int {
	return intFromEnv("LOAD_MAX_CONCURRENCY_CAP", defaultCap)
}

func loadMaxErrorRateFromEnv(defaultRate float64) float64 {
	raw := strings.TrimSpace(os.Getenv("LOAD_MAX_ERROR_RATE"))
	if raw == "" {
		return defaultRate
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return defaultRate
	}
	return value
}

func intFromEnv(key string, defaultValue int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return defaultValue
	}
	return value
}

func durationFromEnvSeconds(key string, defaultValue time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return defaultValue
	}
	return time.Duration(value) * time.Second
}

func durationFromEnvMillis(key string, defaultValue time.Duration, allowZero bool) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || (!allowZero && value == 0) {
		return defaultValue
	}
	return time.Duration(value) * time.Millisecond
}

func clampStepsToCap(steps []int, limit int) []int {
	if limit <= 0 {
		return steps
	}
	out := make([]int, 0, len(steps))
	for _, s := range steps {
		if s <= limit {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		out = append(out, limit)
	}
	return out
}

func maxStep(steps []int) int {
	maximum := 0
	for _, v := range steps {
		if v > maximum {
			maximum = v
		}
	}
	return maximum
}
