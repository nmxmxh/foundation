package httpapi

import (
	"io"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/transfer"
)

// BenchmarkProgressReader measures the per-read overhead the streaming upload
// route adds on top of the raw body copy. The progress reader wraps the body
// and reports the byte high-water mark to the tracker; with the default
// threshold the tracker is touched at most once per threshold window, so the
// steady-state read path must stay allocation-free.
func BenchmarkProgressReader(b *testing.B) {
	tr, err := transfer.NewTracker("tx", "corr", 0)
	if err != nil {
		b.Fatalf("NewTracker: %v", err)
	}
	// One subscriber simulates a live WS progress bridge.
	tr.Subscribe(func(transfer.Update) {})

	const chunk = 32 << 10
	src := &repeatReader{}
	pr := newProgressReader(src, tr, 0)
	buf := make([]byte, chunk)

	b.SetBytes(chunk)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := pr.Read(buf); err != nil {
			b.Fatalf("read: %v", err)
		}
	}
}

// repeatReader is an infinite, allocation-free source of bytes.
type repeatReader struct{ n int64 }

func (r *repeatReader) Read(p []byte) (int, error) {
	r.n += int64(len(p))
	return len(p), nil
}

var _ io.Reader = (*repeatReader)(nil)
