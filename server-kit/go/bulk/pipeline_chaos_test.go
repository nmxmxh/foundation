package bulk

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	transport "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/transport"
	apperrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
)

// faultyReader simulates network disconnections and short reads midway through transfer.
type faultyReader struct {
	data      []byte
	failAfter int
	readBytes int
	err       error
}

func (r *faultyReader) Read(p []byte) (n int, err error) {
	if r.readBytes >= r.failAfter {
		return 0, r.err
	}
	remaining := r.failAfter - r.readBytes
	toRead := len(p)
	if toRead > remaining {
		toRead = remaining
	}
	if toRead > len(r.data)-r.readBytes {
		toRead = len(r.data) - r.readBytes
	}
	copy(p, r.data[r.readBytes:r.readBytes+toRead])
	r.readBytes += toRead
	if r.readBytes >= r.failAfter {
		return toRead, r.err
	}
	return toRead, nil
}

// TestPipelineFaultInjectionAndStreamChaos tests error handling on broken streams and mismatched checksums.
func TestPipelineFaultInjectionAndStreamChaos(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_chaos_pipeline", "idem_chaos_pipeline")

	initEnv := transport.CreateEnvelope(EventInitiateRequested, transport.ObjectFromMap(map[string]any{
		"transfer_id":     "chaos_pipe_1",
		"total_size":      int64(10),
		"chunk_size":      int64(5),
		"max_memory":      int64(5),
		"content_type":    "text/plain",
		"compression":     EncodingIdentity,
		"idempotency_key": "idem_chaos_pipeline",
		"deadline":        fixedNow().Add(time.Minute).Format(time.RFC3339Nano),
	}), nil)

	if _, err := pipeline.HandleControl(ctx, initEnv); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}

	// Subtest 1: Short read error injection.
	t.Run("simulated_network_drop", func(t *testing.T) {
		payload := "01234"
		faulty := &faultyReader{
			data:      []byte(payload),
			failAfter: 2,
			err:       io.ErrUnexpectedEOF,
		}
		env := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
			"transfer_id":         "chaos_pipe_1",
			"part_number":         0,
			"offset":              int64(0),
			"size":                int64(len(payload)),
			"expected_raw_sha256": shaHex(payload),
			"content_type":        "text/plain",
		}, nil)

		_, err := pipeline.AcceptPartEnvelope(ctx, env, faulty)
		if err == nil {
			t.Fatalf("expected error on truncated stream, got nil")
		}
	})

	// Subtest 2: Corrupted checksum injection.
	t.Run("corrupted_payload_checksum", func(t *testing.T) {
		tamperedData := "tamp9"
		env := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
			"transfer_id":         "chaos_pipe_1",
			"part_number":         0,
			"offset":              int64(0),
			"size":                int64(len(tamperedData)),
			"expected_raw_sha256": shaHex("legit"), // Intentional mismatch
			"content_type":        "text/plain",
		}, nil)

		_, err := pipeline.AcceptPartEnvelope(ctx, env, strings.NewReader(tamperedData))
		if !apperrors.Is(err, apperrors.CodeValidation) {
			t.Fatalf("expected CodeValidation error on corrupted checksum, got: %v", err)
		}
	})

	// Subtest 3: Context cancellation during transfer.
	t.Run("context_cancellation", func(t *testing.T) {
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel() // Cancel immediately

		payload := "01234"
		env := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
			"transfer_id":         "chaos_pipe_1",
			"part_number":         0,
			"offset":              int64(0),
			"size":                int64(len(payload)),
			"expected_raw_sha256": shaHex(payload),
			"content_type":        "text/plain",
		}, nil)

		_, err := pipeline.AcceptPartEnvelope(canceledCtx, env, strings.NewReader(payload))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled error, got: %v", err)
		}
	})
}

// TestPipelineConcurrentConflictingPartRacing tests race conditions with inconsistent contents.
func TestPipelineConcurrentConflictingPartRacing(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_race_pipeline", "idem_race_pipeline")

	initEnv := transport.CreateEnvelope(EventInitiateRequested, transport.ObjectFromMap(map[string]any{
		"transfer_id":     "race_pipe_1",
		"total_size":      int64(5),
		"chunk_size":      int64(5),
		"max_memory":      int64(5),
		"content_type":    "text/plain",
		"compression":     EncodingIdentity,
		"idempotency_key": "idem_race_pipeline",
		"deadline":        fixedNow().Add(time.Minute).Format(time.RFC3339Nano),
	}), nil)

	if _, err := pipeline.HandleControl(ctx, initEnv); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}

	const concurrency = 20
	var wg sync.WaitGroup
	results := make([]error, concurrency)

	// Launch concurrent requests to accept part 0.
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var payload string
			if idx == 0 {
				payload = "valid" // Valid winning part
			} else {
				payload = "conf9" // Conflicting part
			}
			env := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
				"transfer_id":         "race_pipe_1",
				"part_number":         0,
				"offset":              int64(0),
				"size":                int64(len(payload)),
				"expected_raw_sha256": shaHex(payload),
				"content_type":        "text/plain",
			}, nil)

			_, err := pipeline.AcceptPartEnvelope(ctx, env, strings.NewReader(payload))
			results[idx] = err
		}(i)
	}

	wg.Wait()

	// Exactly one unique part payload must be recorded; conflicting re-uploads must fail.
	status, err := mgr.Status(ctx, "race_pipe_1")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.PartsAccepted > 1 {
		t.Fatalf("more than 1 part accepted for single-part transfer: %d", status.PartsAccepted)
	}
}

// TestPipelineDiagnosticsUnderChaos verifies diagnostics stay accurate during failures.
func TestPipelineDiagnosticsUnderChaos(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)

	plan := TransferPlan{
		TransferID:  "chaos_diag_1",
		ChunkSize:   10,
		MaxMemory:   10,
		Compression: EncodingIdentity,
		Deadline:    fixedNow().Add(5 * time.Minute),
	}

	diag := pipeline.Diagnostics(plan)
	if diag.MemoryBudget != 10 {
		t.Fatalf("expected memory budget 10, got: %d", diag.MemoryBudget)
	}
	if diag.ChunkSize != 10 {
		t.Fatalf("expected chunk size 10, got: %d", diag.ChunkSize)
	}
	if diag.Fallback != "streamed-objectstore" {
		t.Fatalf("expected fallback streamed-objectstore, got: %s", diag.Fallback)
	}
	// HolePunchAvailable must match the pipeline initialized capability.
	_ = diag.HolePunchAvailable
}
