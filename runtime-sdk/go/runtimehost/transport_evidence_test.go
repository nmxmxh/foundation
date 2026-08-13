//go:build linux || darwin

package runtimehost

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go/runtimehost/generated"
)

// Evidence for the transport ladder, measured against a real child process.
//
// Foundation's other process tests re-exec the test binary as a fake kernel.
// That is correct for protocol coverage and worthless for latency: the fake's
// pages are already resident, and it is the same binary the test runs in. Every
// transport number this project had therefore came from an application repo,
// which is the wrong place to keep evidence about foundation's own transport.
//
// These run against runtime-sdk/rust/crates/ovrt-native/src/bin/reference_kernel.rs:
//
//	cargo build --release -p ovrt-native --bin reference_kernel \
//	  --manifest-path runtime-sdk/rust/Cargo.toml
//	OVRT_REFERENCE_KERNEL=$PWD/runtime-sdk/rust/target/release/reference_kernel \
//	  go test ./runtimehost/ -run 'Soak|Sigkill' -count=1
//
// Skipped without the variable rather than building cargo from a Go test: a
// test that shells out to a compiler is slow, fails for reasons unrelated to
// what it asserts, and hides which binary it measured.
const referenceKernelEnv = "OVRT_REFERENCE_KERNEL"

func referenceKernelPath(tb testing.TB) string {
	tb.Helper()
	path := strings.TrimSpace(os.Getenv(referenceKernelEnv))
	if path == "" {
		tb.Skipf("%s is not set; build reference_kernel and point this at it", referenceKernelEnv)
	}
	if _, err := os.Stat(path); err != nil {
		tb.Fatalf("%s=%q is not usable: %v", referenceKernelEnv, path, err)
	}
	if !sharedMemorySupported("") {
		tb.Skip("shared memory transport is not supported on this runtime")
	}
	return path
}

func referenceKernelPool(tb testing.TB, mode ProcessTransportMode) *ProcessPool {
	tb.Helper()
	pool, err := NewProcessPool(ProcessPoolOptions{
		Command:         []string{referenceKernelPath(tb)},
		Workers:         1,
		Transport:       mode,
		ExchangeTimeout: 10 * time.Second,
		WarmupUnitID:    "runtime.echo",
	})
	if err != nil {
		tb.Fatalf("NewProcessPool(%s) error = %v", mode, err)
	}
	tb.Cleanup(func() { _ = pool.Close() })
	return pool
}

func benchmarkTransport(b *testing.B, mode ProcessTransportMode) {
	pool := referenceKernelPool(b, mode)
	req := ProcessRequest{UnitID: "runtime.echo", Input: []byte("transport")}
	dst := make([]byte, generated.OUTPUT_MAX_BYTES)

	// Warm before the timer. A cold child costs 17x its warm price, and a
	// benchmark that includes that is measuring page faults. NewProcessPool
	// already runs one warm-up exchange; this covers the rest of the path.
	for range 200 {
		if _, err := pool.ExecuteInto(context.Background(), req, dst); err != nil {
			b.Fatalf("warmup ExecuteInto() error = %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := pool.ExecuteInto(context.Background(), req, dst); err != nil {
			b.Fatalf("ExecuteInto() error = %v", err)
		}
	}
}

func BenchmarkTransportStdio(b *testing.B) {
	benchmarkTransport(b, ProcessTransportStdio)
}

func BenchmarkTransportSharedMemory(b *testing.B) {
	benchmarkTransport(b, ProcessTransportSharedMemory)
}

func BenchmarkTransportSharedMemoryEpoch(b *testing.B) {
	benchmarkTransport(b, ProcessTransportSharedMemoryEpoch)
}

// An epoch is a change marker, and the failure that matters is a missed one:
// the host publishes, the kernel does not observe it, and the caller waits out
// its timeout. That failure is rate-dependent and does not reproduce in a
// handful of calls, so this runs the exchange enough times to expose it.
//
// Every response is checked, not just the last: a lost epoch shows up as one
// exchange returning the *previous* result rather than as an error, which a
// test that only counted successes would pass straight through.
func TestEpochTransportSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("soak is skipped under -short")
	}
	pool := referenceKernelPool(t, ProcessTransportSharedMemoryEpoch)

	// 200k by default so the suite stays runnable; the 10^6 acceptance run is
	// OVRT_SOAK_EXCHANGES=1000000.
	exchanges := 200_000
	if raw := strings.TrimSpace(os.Getenv("OVRT_SOAK_EXCHANGES")); raw != "" {
		if n, err := parseCount(raw); err == nil && n > 0 {
			exchanges = n
		}
	}

	dst := make([]byte, generated.OUTPUT_MAX_BYTES)
	started := time.Now()
	for i := range exchanges {
		// A payload that changes every call. With a constant input a lost epoch
		// returns the previous result, which is byte-identical to the expected
		// one — the soak would pass while dropping exchanges.
		input := []byte("soak-" + itoa(i))
		response, err := pool.ExecuteInto(context.Background(), ProcessRequest{
			UnitID: "runtime.echo",
			Input:  input,
		}, dst)
		if err != nil {
			t.Fatalf("exchange %d of %d failed after %v: %v", i, exchanges, time.Since(started), err)
		}
		want := strings.ToUpper(string(input))
		if string(response.Output) != want {
			t.Fatalf("exchange %d returned %q, want %q — an epoch was missed and the previous result was read",
				i, response.Output, want)
		}
	}
	t.Logf("%d exchanges in %v (%.0f ns/op)", exchanges, time.Since(started),
		float64(time.Since(started).Nanoseconds())/float64(exchanges))
}

// The liveness hole, end to end: kill the child mid-flight and the next
// exchange must fail inside one timeout rather than blocking for it.
//
// Under stdio and shm this is free — the blocking read fails. Under the epoch
// transport nothing fails on its own, because a dead kernel and a slow one
// write the same nothing to the slot. This asserts the supervisor closes that
// gap, with a deliberately long exchange timeout so a test that passes by
// timing out cannot be mistaken for one that passes by detection.
func TestEpochTransportDetectsASigkilledKernel(t *testing.T) {
	path := referenceKernelPath(t)
	pool, err := NewProcessPool(ProcessPoolOptions{
		Command:   []string{path},
		Workers:   1,
		Transport: ProcessTransportSharedMemoryEpoch,
		// An hour. If the kill goes undetected this test hangs rather than
		// passing slowly, which is the honest outcome.
		ExchangeTimeout: time.Hour,
		WarmupUnitID:    "runtime.echo",
	})
	if err != nil {
		t.Fatalf("NewProcessPool() error = %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	dst := make([]byte, generated.OUTPUT_MAX_BYTES)
	req := ProcessRequest{UnitID: "runtime.echo", Input: []byte("alive")}
	if _, err := pool.ExecuteInto(context.Background(), req, dst); err != nil {
		t.Fatalf("baseline exchange failed: %v", err)
	}

	worker := pool.allWorkers[0]
	worker.mu.Lock()
	process := worker.cmd.Process
	worker.mu.Unlock()
	if process == nil {
		t.Fatal("worker has no process to kill")
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}

	// The supervisor has to observe the exit before the wait can consult it.
	deadline := time.Now().Add(5 * time.Second)
	for worker.childAlive() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if worker.childAlive() {
		t.Fatal("supervisor did not observe the killed child")
	}

	// The pool restarts a failed worker, so the next call may well succeed —
	// what must not happen is a call that hangs. Bound it independently of the
	// pool's own timeout.
	done := make(chan error, 1)
	go func() {
		_, err := pool.ExecuteInto(context.Background(), req, make([]byte, generated.OUTPUT_MAX_BYTES))
		done <- err
	}()
	select {
	case err := <-done:
		// Either outcome is correct: a clean restart, or a reported loss. A
		// hang is the only failure.
		if err != nil && !errors.Is(err, errEpochPeerLost) {
			t.Logf("post-kill exchange reported %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("exchange after SIGKILL did not return; the kill went undetected")
	}
}

// Two workers, both under the epoch transport, to prove the doorbell is
// per-segment. A shared slot would show up as one worker consuming the other's
// epoch and both stalling.
func TestEpochTransportKeepsWorkersIndependent(t *testing.T) {
	path := referenceKernelPath(t)
	pool, err := NewProcessPool(ProcessPoolOptions{
		Command:         []string{path},
		Workers:         2,
		Transport:       ProcessTransportSharedMemoryEpoch,
		ExchangeTimeout: 10 * time.Second,
		WarmupUnitID:    "runtime.echo",
	})
	if err != nil {
		t.Fatalf("NewProcessPool() error = %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	var failures atomic.Int32
	done := make(chan struct{})
	for worker := range 2 {
		go func(index int) {
			defer func() { done <- struct{}{} }()
			dst := make([]byte, generated.OUTPUT_MAX_BYTES)
			for i := range 500 {
				input := []byte("w" + itoa(index) + "-" + itoa(i))
				response, err := pool.ExecuteInto(context.Background(), ProcessRequest{
					UnitID: "runtime.echo",
					Input:  input,
				}, dst)
				if err != nil || string(response.Output) != strings.ToUpper(string(input)) {
					failures.Add(1)
					return
				}
			}
		}(worker)
	}
	for range 2 {
		<-done
	}
	if failures.Load() != 0 {
		t.Fatalf("%d concurrent worker streams failed", failures.Load())
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 12)
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

func parseCount(raw string) (int, error) {
	total := 0
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, errors.New("not a count")
		}
		total = total*10 + int(character-'0')
	}
	return total, nil
}

// The crossover: where the epoch doorbell stops winning.
//
// An echo unit flatters this transport by construction. The doorbell wins by
// spinning until the reply lands, and with zero compute the reply always lands
// inside the spin — so a benchmark against echo alone produces a number that is
// true and misleading, and that is how a transport gets adopted into a pool it
// makes slower. Downstream measurement found exactly that: 3x faster for a
// microsecond-scale unit, 38% *slower* for one taking ~570us per call.
//
// The mechanism is not subtle. waitForEpochChange spins, then sleeps with
// exponential backoff. When the kernel is busy far longer than the spin budget
// the spin is wasted and the backoff then overshoots the reply, so the host
// wakes late. A pipe read blocks once and is woken by the write; it cannot
// overshoot.
//
// Sweeps runtime.busy across service times so the crossover is visible here
// rather than in an application repo.
func BenchmarkTransportServiceTimeSweep(b *testing.B) {
	for _, micros := range []uint32{0, 10, 100, 500, 2000} {
		for _, mode := range []ProcessTransportMode{
			ProcessTransportSharedMemory,
			ProcessTransportSharedMemoryEpoch,
		} {
			name := itoa(int(micros)) + "us/" + string(mode)
			b.Run(name, func(b *testing.B) {
				benchmarkBusyTransport(b, mode, micros, EpochWaitTuning{})
			})
		}
	}
}

// The same sweep with the spin disabled and the backoff capped near the
// service time, which is what EpochWaitTuning exists for. If tuning does not
// recover the long-service-time case, the honest advice is "use the pipe" and
// the knob is decoration.
func BenchmarkTransportServiceTimeSweepTuned(b *testing.B) {
	for _, micros := range []uint32{500, 2000} {
		// Several caps, because a single choice cannot distinguish "tuning does
		// not help" from "that was the wrong cap". A sleep ladder overshoots the
		// reply by up to MaxSleep, so a tighter cap trades wakeups for accuracy
		// and the sweep shows whether any point on that trade beats the pipe.
		for _, divisor := range []int{4, 20, 100} {
			cap := time.Duration(micros) * time.Microsecond / time.Duration(divisor)
			name := itoa(int(micros)) + "us/cap-" + itoa(int(cap/time.Microsecond)) + "us"
			b.Run(name, func(b *testing.B) {
				benchmarkBusyTransport(b, ProcessTransportSharedMemoryEpoch, micros, EpochWaitTuning{
					SpinIterations: -1,
					MaxSleep:       cap,
				})
			})
		}
	}
}

func benchmarkBusyTransport(b *testing.B, mode ProcessTransportMode, micros uint32, tuning EpochWaitTuning) {
	path := referenceKernelPath(b)
	pool, err := NewProcessPool(ProcessPoolOptions{
		Command:         []string{path},
		Workers:         1,
		Transport:       mode,
		ExchangeTimeout: 30 * time.Second,
		WarmupUnitID:    "runtime.busy",
		EpochWaitTuning: tuning,
	})
	if err != nil {
		b.Fatalf("NewProcessPool(%s) error = %v", mode, err)
	}
	b.Cleanup(func() { _ = pool.Close() })

	input := []byte{byte(micros), byte(micros >> 8), byte(micros >> 16), byte(micros >> 24)}
	req := ProcessRequest{UnitID: "runtime.busy", Input: input}
	dst := make([]byte, generated.OUTPUT_MAX_BYTES)

	for range 50 {
		if _, err := pool.ExecuteInto(context.Background(), req, dst); err != nil {
			b.Fatalf("warmup ExecuteInto() error = %v", err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := pool.ExecuteInto(context.Background(), req, dst); err != nil {
			b.Fatalf("ExecuteInto() error = %v", err)
		}
	}
}

// The tuning has to actually reach the exchange. Its own field doc claimed it
// was per-pool tunable for a while when nothing on ProcessPoolOptions reached
// defaultEpochWaitPolicy — a comment describing an intention rather than the
// code. This pins the wiring.
func TestEpochWaitTuningReachesTheExchange(t *testing.T) {
	worker := &processWorker{
		mode:          ProcessTransportSharedMemoryEpoch,
		shm:           epochTestSegment(t),
		warmupTimeout: time.Minute,
		epochTuning:   EpochWaitTuning{SpinIterations: 7, MaxSleep: 3 * time.Millisecond},
	}
	exchange, ok := worker.exchange().(epochExchange)
	if !ok {
		t.Fatalf("exchange() returned %T, want epochExchange", worker.exchange())
	}
	if exchange.policy.spinIterations != 7 {
		t.Errorf("spinIterations = %d, want 7", exchange.policy.spinIterations)
	}
	if exchange.policy.maxSleep != 3*time.Millisecond {
		t.Errorf("maxSleep = %v, want 3ms", exchange.policy.maxSleep)
	}
	if exchange.policy.timeout != time.Minute {
		t.Errorf("timeout = %v, want 1m", exchange.policy.timeout)
	}
}

// Zero means "use the default"; negative means "none". Without the distinction
// a pool cannot ask for a wait that never spins, which is exactly what a
// millisecond-scale kernel needs.
func TestEpochWaitTuningDistinguishesUnsetFromNone(t *testing.T) {
	if got := (EpochWaitTuning{}).policy(time.Minute).spinIterations; got != DefaultEpochSpinIterations {
		t.Errorf("unset spinIterations = %d, want the %d default", got, DefaultEpochSpinIterations)
	}
	if got := (EpochWaitTuning{SpinIterations: -1}).policy(time.Minute).spinIterations; got != 0 {
		t.Errorf("negative spinIterations = %d, want 0", got)
	}
	if got := (EpochWaitTuning{}).policy(time.Minute).maxSleep; got != DefaultEpochMaxSleep {
		t.Errorf("unset maxSleep = %v, want the %v default", got, DefaultEpochMaxSleep)
	}
}
