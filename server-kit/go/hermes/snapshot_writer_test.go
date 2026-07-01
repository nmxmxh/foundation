package hermes

import (
	"context"
	"testing"
	"time"
)

func TestNewSnapshotWriterValidation(t *testing.T) {
	store := newTestStore(t, snapshotTierSpec())
	snaps := NewMemorySnapshotStore()
	if _, err := NewSnapshotWriter(nil, snaps, SnapshotWriterOptions{DeltaThreshold: 1}); err == nil {
		t.Fatal("expected error for nil store")
	}
	if _, err := NewSnapshotWriter(store, nil, SnapshotWriterOptions{DeltaThreshold: 1}); err == nil {
		t.Fatal("expected error for nil snapshot store")
	}
	if _, err := NewSnapshotWriter(store, snaps, SnapshotWriterOptions{}); err == nil {
		t.Fatal("expected error when neither trigger is set")
	}
}

// TestSnapshotWriterDeltaThreshold: the writer snapshots only once the source
// watermark has advanced by at least the threshold, and skips when nothing new
// has been applied.
func TestSnapshotWriterDeltaThreshold(t *testing.T) {
	const org = "org_1"
	store, _ := seedSnapshotTierStore(t, org, 50) // watermark = 50
	snaps := NewMemorySnapshotStore()
	writer, err := NewSnapshotWriter(store, snaps, SnapshotWriterOptions{DeltaThreshold: 100})
	if err != nil {
		t.Fatalf("NewSnapshotWriter() error = %v", err)
	}
	ctx := context.Background()
	q := Query{OrganizationID: org}

	// 50 < 100: not due.
	if desc, wrote, err := writer.MaybeSnapshot(ctx, "state_ticks", q); err != nil || wrote {
		t.Fatalf("at watermark 50 wrote=%v desc=%+v err=%v, want no snapshot", wrote, desc, err)
	}

	// Advance to 150 (>= threshold): due.
	if _, err := store.ApplyRecords(ctx, "state_ticks", "seed", 51, snapshotTierRecords(org, 50, 100)); err != nil {
		t.Fatalf("ApplyRecords() error = %v", err)
	}
	desc, wrote, err := writer.MaybeSnapshot(ctx, "state_ticks", q)
	if err != nil || !wrote {
		t.Fatalf("at watermark 150 wrote=%v err=%v, want a snapshot", wrote, err)
	}
	if desc.Watermark != 150 || desc.Records != 150 {
		t.Fatalf("snapshot desc = {wm:%d recs:%d}, want {150,150}", desc.Watermark, desc.Records)
	}

	// No new applies: skipped.
	if _, wrote, err := writer.MaybeSnapshot(ctx, "state_ticks", q); err != nil || wrote {
		t.Fatalf("with no new applies wrote=%v err=%v, want skip", wrote, err)
	}

	// Advance by another full threshold: due again.
	if _, err := store.ApplyRecords(ctx, "state_ticks", "seed", 151, snapshotTierRecords(org, 150, 100)); err != nil {
		t.Fatalf("ApplyRecords() error = %v", err)
	}
	if desc, wrote, err := writer.MaybeSnapshot(ctx, "state_ticks", q); err != nil || !wrote || desc.Watermark != 250 {
		t.Fatalf("at watermark 250 wrote=%v desc.wm=%d err=%v, want snapshot@250", wrote, desc.Watermark, err)
	}
}

// TestSnapshotWriterInterval: with a time trigger, the writer snapshots on first
// observation and then only after the interval elapses.
func TestSnapshotWriterInterval(t *testing.T) {
	const org = "org_1"
	store, _ := seedSnapshotTierStore(t, org, 10)
	snaps := NewMemorySnapshotStore()
	clock := time.Unix(1_000_000, 0)
	writer, err := NewSnapshotWriter(store, snaps, SnapshotWriterOptions{
		Interval: time.Minute,
		Now:      func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewSnapshotWriter() error = %v", err)
	}
	ctx := context.Background()
	q := Query{OrganizationID: org}

	// First observation: baseline snapshot.
	if _, wrote, err := writer.MaybeSnapshot(ctx, "state_ticks", q); err != nil || !wrote {
		t.Fatalf("first observation wrote=%v err=%v, want baseline snapshot", wrote, err)
	}

	// 30s later with new applies: interval not elapsed, skip.
	clock = clock.Add(30 * time.Second)
	if _, err := store.ApplyRecords(ctx, "state_ticks", "seed", 11, snapshotTierRecords(org, 10, 10)); err != nil {
		t.Fatalf("ApplyRecords() error = %v", err)
	}
	if _, wrote, err := writer.MaybeSnapshot(ctx, "state_ticks", q); err != nil || wrote {
		t.Fatalf("30s in wrote=%v err=%v, want skip (interval not elapsed)", wrote, err)
	}

	// Past the interval: due.
	clock = clock.Add(31 * time.Second)
	if _, wrote, err := writer.MaybeSnapshot(ctx, "state_ticks", q); err != nil || !wrote {
		t.Fatalf("past interval wrote=%v err=%v, want snapshot", wrote, err)
	}
}

// TestSnapshotWriterEmptyProjectionSkipped: a projection with watermark 0 is
// never snapshotted.
func TestSnapshotWriterEmptyProjectionSkipped(t *testing.T) {
	store := newTestStore(t, snapshotTierSpec())
	snaps := NewMemorySnapshotStore()
	writer, err := NewSnapshotWriter(store, snaps, SnapshotWriterOptions{DeltaThreshold: 1, Interval: time.Second})
	if err != nil {
		t.Fatalf("NewSnapshotWriter() error = %v", err)
	}
	if _, wrote, err := writer.MaybeSnapshot(context.Background(), "state_ticks", Query{OrganizationID: "org_1"}); err != nil || wrote {
		t.Fatalf("empty projection wrote=%v err=%v, want skip", wrote, err)
	}
}
