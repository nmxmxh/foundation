package events

import (
	"testing"
	"time"
)

// Encode-path benchmarks for the event envelope.
//
// ToBinary is on every publish path, so its allocation profile is the one that
// shows up as GC pressure under load. It used to take a defensive copy of the
// whole payload before handing it to proto.Marshal, which retains nothing and
// copies into its own output buffer — so the copy bought no safety and doubled
// the bytes touched per encode. Measured on an M1 Pro before and after removing
// it:
//
//	payload   before                     after
//	   256B    2013 ns    2912 B/13     1983 ns    2656 B/12
//	    4KB    3048 ns   11264 B/13     2480 ns    7168 B/12
//	   64KB   15449 ns  141569 B/13     9285 ns   76032 B/12
//
// The win scales with payload size, which is the point: a large payload is
// exactly when the wasted copy hurt most.

func benchEnvelope(payloadBytes int) Envelope {
	payload := make([]byte, payloadBytes)
	for i := range payload {
		payload[i] = byte(i)
	}
	return Envelope{
		ID:              "evt_1",
		EventType:       "media:upload:requested",
		PayloadBytes:    payload,
		PayloadEncoding: PayloadEncodingBinary,
		CorrelationID:   "corr_1",
		SchemaVersion:   "1.0",
		Timestamp:       time.Unix(1700000000, 0).UTC(),
	}
}

func BenchmarkEnvelopeToBinary(b *testing.B) {
	for _, size := range []int{256, 4096, 65536} {
		env := benchEnvelope(size)
		b.Run(sizeLabel(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				raw, err := env.ToBinary()
				if err != nil {
					b.Fatalf("ToBinary: %v", err)
				}
				if len(raw) == 0 {
					b.Fatal("empty")
				}
			}
		})
	}
}

func sizeLabel(n int) string {
	switch n {
	case 256:
		return "payload256B"
	case 4096:
		return "payload4KB"
	default:
		return "payload64KB"
	}
}
