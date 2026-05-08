//nolint:gosec // Load tests use math/rand for traffic mix only; no security decisions depend on it.
package load

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"{{MODULE_PATH}}/tests/testutil"
)

type loadProfile struct {
	Class          string
	CPUCores       int
	Steps          []int
	StepDuration   time.Duration
	PrepareTimeout time.Duration
	ThinkTime      time.Duration
	OpTimeout      time.Duration
}

type operation int

const (
	opDBRead operation = iota
	opDBWrite
	opRedis
	opQueueInspect
	opCount
)

var operationNames = [opCount]string{
	"db_read",
	"db_write",
	"redis",
	"queue",
}

// TestRecurringInfrastructureLoad exercises the recurring scaffold paths every
// backend project depends on: Postgres pool acquisition, bounded writes, Redis
// hot-key access, and queue table visibility. It is intentionally opt-in.
func TestRecurringInfrastructureLoad(t *testing.T) {
	if os.Getenv("RUN_LOAD_TESTS") != "1" {
		t.Skip("skipping load test by default; set RUN_LOAD_TESTS=1 to execute")
	}
	if testing.Short() {
		t.Skip("skipping load test in short mode")
	}

	cores := runtime.NumCPU()
	t.Setenv("TEST_DB_MAX_CONNS", strconv.Itoa(inferLoadDBMaxConns(cores)))

	env := testutil.SetupRealTestEnv(t)
	ctx := context.Background()
	profile := inferLoadProfile()

	steps := loadStepsFromEnv(profile.Steps)
	if cap := loadMaxConcurrencyCapFromEnv(0); cap > 0 {
		steps = clampStepsToCap(steps, cap)
	}
	stepDuration := loadStepDurationFromEnv(profile.StepDuration)
	thinkTime := loadThinkTimeFromEnv(profile.ThinkTime)
	opTimeout := loadOpTimeoutFromEnv(profile.OpTimeout)
	mix := loadMixFromEnv()

	fmt.Println("\n--- Starting Scaffold Recurring Infrastructure Load Test ---")
	fmt.Printf("Profile: class=%s cpu_cores=%d\n", profile.Class, profile.CPUCores)
	fmt.Printf("Steps (Concurrency): %v\n", steps)
	fmt.Printf("Duration per step: %v\n", stepDuration)
	fmt.Printf("Op timeout: %v\n", opTimeout)
	fmt.Printf("Think time: %v\n", thinkTime)
	fmt.Printf("Mix weights: db_read=%d db_write=%d redis=%d queue=%d\n", mix.DBReadWeight, mix.DBWriteWeight, mix.RedisWeight, mix.QueueWeight)

	for _, concurrency := range steps {
		runLoadStep(t, ctx, env, concurrency, stepDuration, thinkTime, opTimeout, mix)
	}

	fmt.Println("\n--- Scaffold Recurring Infrastructure Load Test Complete ---")
}

type loadMix struct {
	DBReadWeight  int
	DBWriteWeight int
	RedisWeight   int
	QueueWeight   int
}

func runLoadStep(t *testing.T, ctx context.Context, env *testutil.RealTestEnv, concurrency int, duration, thinkTime, opTimeout time.Duration, mix loadMix) {
	t.Helper()
	fmt.Printf("\n[Scaffold Step] Concurrency: %d\n", concurrency)

	var attempts [opCount]int64
	var errors [opCount]int64
	var latencyMicros [opCount]int64
	var totalSuccess int64
	var totalError int64
	var totalLatency int64

	queueBefore := fetchRiverStateCounts(ctx, env)
	redisBefore := fetchRedisPingLatency(ctx, env)

	stop := make(chan struct{})
	time.AfterFunc(duration, func() {
		close(stop)
	})

	start := time.Now()
	wg := sync.WaitGroup{}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerIdx int) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerIdx)*1009))
			for {
				select {
				case <-stop:
					return
				default:
				}

				op := chooseOperation(rnd, mix)
				opCtx, cancel := context.WithTimeout(ctx, opTimeout)
				opStart := time.Now()
				err := executeOperation(opCtx, env, rnd, op)
				cancel()

				durationMicros := time.Since(opStart).Microseconds()
				atomic.AddInt64(&attempts[op], 1)
				atomic.AddInt64(&latencyMicros[op], durationMicros)
				atomic.AddInt64(&totalLatency, durationMicros)
				if err != nil {
					atomic.AddInt64(&errors[op], 1)
					atomic.AddInt64(&totalError, 1)
				} else {
					atomic.AddInt64(&totalSuccess, 1)
				}

				if thinkTime > 0 {
					time.Sleep(thinkTime)
				}
			}
		}(i)
	}
	wg.Wait()

	elapsed := time.Since(start).Seconds()
	totalOps := totalSuccess + totalError
	if totalOps == 0 {
		t.Fatalf("no operations executed at concurrency %d", concurrency)
	}

	queueAfter := fetchRiverStateCounts(ctx, env)
	redisAfter := fetchRedisPingLatency(ctx, env)
	rps := float64(totalOps) / elapsed
	avgLatencyMs := float64(totalLatency) / float64(totalOps) / 1000.0
	errorRate := float64(totalError) / float64(totalOps) * 100.0

	fmt.Printf("  Duration: %.2fs\n", elapsed)
	fmt.Printf("  Total Ops: %d\n", totalOps)
	fmt.Printf("  RPS: %.2f\n", rps)
	fmt.Printf("  Avg Latency: %.2f ms\n", avgLatencyMs)
	fmt.Printf("  Error Rate: %.2f%%\n", errorRate)
	for op := operation(0); op < opCount; op++ {
		opAttempts := atomic.LoadInt64(&attempts[op])
		if opAttempts == 0 {
			continue
		}
		opErrors := atomic.LoadInt64(&errors[op])
		opLatencyMs := float64(atomic.LoadInt64(&latencyMicros[op])) / float64(opAttempts) / 1000.0
		opErrorRate := float64(opErrors) / float64(opAttempts) * 100.0
		fmt.Printf("    - %-8s ops=%-6d avg=%.2fms err=%.2f%%\n", operationNames[op], opAttempts, opLatencyMs, opErrorRate)
	}
	fmt.Printf(
		"  River state delta: available=%+d running=%+d completed=%+d retryable=%+d discarded=%+d\n",
		queueAfter["available"]-queueBefore["available"],
		queueAfter["running"]-queueBefore["running"],
		queueAfter["completed"]-queueBefore["completed"],
		queueAfter["retryable"]-queueBefore["retryable"],
		queueAfter["discarded"]-queueBefore["discarded"],
	)
	fmt.Printf("  Redis ping latency: before=%s after=%s\n", redisBefore, redisAfter)

	if errorRate > loadMaxErrorRateFromEnv(2.0) {
		t.Errorf("high error rate at concurrency %d: %.2f%%", concurrency, errorRate)
	}
}

func executeOperation(ctx context.Context, env *testutil.RealTestEnv, rnd *rand.Rand, op operation) error {
	switch op {
	case opDBRead:
		var now time.Time
		return env.DB.QueryRow(ctx, "SELECT NOW()").Scan(&now)
	case opDBWrite:
		tx, err := env.DB.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()
		_, err = tx.Exec(ctx, "CREATE TEMP TABLE IF NOT EXISTS scaffold_load_events (id BIGSERIAL PRIMARY KEY, worker_key TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()) ON COMMIT PRESERVE ROWS")
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "INSERT INTO scaffold_load_events (worker_key) VALUES ($1)", fmt.Sprintf("w-%d", rnd.Intn(1024)))
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	case opRedis:
		if env.Redis == nil {
			return nil
		}
		key := fmt.Sprintf("load:scaffold:%d", rnd.Intn(256))
		value := strconv.FormatInt(time.Now().UnixNano(), 10)
		if err := env.Redis.Set(ctx, key, value, 2*time.Minute).Err(); err != nil {
			return err
		}
		_, err := env.Redis.Get(ctx, key).Result()
		return err
	case opQueueInspect:
		_ = fetchRiverStateCounts(ctx, env)
		return nil
	default:
		return nil
	}
}

func fetchRiverStateCounts(ctx context.Context, env *testutil.RealTestEnv) map[string]int64 {
	counts := map[string]int64{
		"available": 0,
		"running":   0,
		"completed": 0,
		"retryable": 0,
		"discarded": 0,
	}
	rows, err := env.DB.Query(ctx, "SELECT state, COUNT(*) FROM river_job GROUP BY state")
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || err == pgx.ErrNoRows {
			return counts
		}
		return counts
	}
	defer rows.Close()

	for rows.Next() {
		var state string
		var count int64
		if scanErr := rows.Scan(&state, &count); scanErr != nil {
			continue
		}
		counts[state] = count
	}
	return counts
}

func fetchRedisPingLatency(ctx context.Context, env *testutil.RealTestEnv) time.Duration {
	if env.Redis == nil {
		return -1
	}
	start := time.Now()
	if err := env.Redis.Ping(ctx).Err(); err != nil {
		return -1
	}
	return time.Since(start)
}

func chooseOperation(r *rand.Rand, mix loadMix) operation {
	total := mix.DBReadWeight + mix.DBWriteWeight + mix.RedisWeight + mix.QueueWeight
	if total <= 0 {
		return opDBRead
	}
	pick := r.Intn(total)
	if pick < mix.DBReadWeight {
		return opDBRead
	}
	pick -= mix.DBReadWeight
	if pick < mix.DBWriteWeight {
		return opDBWrite
	}
	pick -= mix.DBWriteWeight
	if pick < mix.RedisWeight {
		return opRedis
	}
	return opQueueInspect
}

func inferLoadProfile() loadProfile {
	return inferLoadProfileForCores(runtime.NumCPU())
}

func inferLoadProfileForCores(cores int) loadProfile {
	if cores <= 0 {
		cores = 1
	}
	switch {
	case cores <= 4:
		return loadProfile{Class: "small", CPUCores: cores, Steps: []int{8, 16, 32}, StepDuration: 6 * time.Second, PrepareTimeout: 90 * time.Second, ThinkTime: 10 * time.Millisecond, OpTimeout: 2500 * time.Millisecond}
	case cores <= 8:
		return loadProfile{Class: "medium", CPUCores: cores, Steps: []int{16, 32, 64}, StepDuration: 8 * time.Second, PrepareTimeout: 120 * time.Second, ThinkTime: 8 * time.Millisecond, OpTimeout: 2200 * time.Millisecond}
	case cores <= 16:
		return loadProfile{Class: "large", CPUCores: cores, Steps: []int{32, 64, 128}, StepDuration: 10 * time.Second, PrepareTimeout: 180 * time.Second, ThinkTime: 5 * time.Millisecond, OpTimeout: 2000 * time.Millisecond}
	default:
		return loadProfile{Class: "xlarge", CPUCores: cores, Steps: []int{64, 128, 256}, StepDuration: 12 * time.Second, PrepareTimeout: 240 * time.Second, ThinkTime: 4 * time.Millisecond, OpTimeout: 1800 * time.Millisecond}
	}
}

func inferLoadDBMaxConns(cores int) int {
	if cores <= 0 {
		cores = 1
	}
	value := cores * 8
	if value < 16 {
		value = 16
	}
	if value > 120 {
		value = 120
	}
	return value
}

func loadMixFromEnv() loadMix {
	mix := loadMix{
		DBReadWeight:  35,
		DBWriteWeight: 35,
		RedisWeight:   20,
		QueueWeight:   10,
	}
	mix.DBReadWeight = intFromEnv("LOAD_DB_READ_WEIGHT", mix.DBReadWeight)
	mix.DBWriteWeight = intFromEnv("LOAD_DB_WRITE_WEIGHT", mix.DBWriteWeight)
	mix.RedisWeight = intFromEnv("LOAD_REDIS_WEIGHT", mix.RedisWeight)
	mix.QueueWeight = intFromEnv("LOAD_QUEUE_WEIGHT", mix.QueueWeight)
	if mix.DBReadWeight+mix.DBWriteWeight+mix.RedisWeight+mix.QueueWeight <= 0 {
		return loadMix{DBReadWeight: 35, DBWriteWeight: 35, RedisWeight: 20, QueueWeight: 10}
	}
	return mix
}
