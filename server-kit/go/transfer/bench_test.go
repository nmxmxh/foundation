package transfer

import (
	"context"
	"testing"
)

// BenchmarkTrackerAdvance measures the progress-lane hot path. Advancing a
// tracker is invoked once per accepted read chunk during an upload, so it must
// stay cheap and ideally allocation-free for the common subscriber counts.
func BenchmarkTrackerAdvance(b *testing.B) {
	cases := []struct {
		name string
		subs int
	}{
		{"subs=0", 0},
		{"subs=1", 1},
		{"subs=8", 8},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			tr := newTracker("tx", "corr", 0, fixedClock())
			for range tc.subs {
				tr.Subscribe(func(Update) {})
			}
			b.ReportAllocs()
			b.ResetTimer()
			var offset int64
			for i := 0; i < b.N; i++ {
				offset++
				_, _ = tr.Advance(offset)
			}
		})
	}
}

// BenchmarkTrackerSnapshot measures the read path used by HEAD/status handlers.
func BenchmarkTrackerSnapshot(b *testing.B) {
	tr := newTracker("tx", "corr", 1000, fixedClock())
	_, _ = tr.Advance(500)
	b.ReportAllocs()
	for b.Loop() {
		_ = tr.Snapshot()
	}
}

// BenchmarkManagerBeginComplete measures a full bracketed lifecycle against the
// in-memory bus: the bookend emission cost plus registry churn.
func BenchmarkManagerBeginComplete(b *testing.B) {
	ctx := context.Background()
	m, err := NewManager(Config{Domain: "media", Action: "upload", Version: "v1"})
	if err != nil {
		b.Fatalf("NewManager: %v", err)
	}
	b.ReportAllocs()
	for b.Loop() {
		tr, err := m.Begin(ctx, BeginInput{TransferID: "tx", CorrelationID: "corr", BytesTotal: 1024})
		if err != nil {
			b.Fatalf("Begin: %v", err)
		}
		_, _ = tr.Advance(1024)
		if err := m.Complete(ctx, "tx", "sum"); err != nil {
			b.Fatalf("Complete: %v", err)
		}
	}
}
