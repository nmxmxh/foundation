//go:build foundation_movement

package hermes

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

var movementCaptureMu sync.Mutex

var movementMetricNames = [movementCounterCount]string{
	"numeric_read_bytes/op", "numeric_write_bytes/op",
	"bitmap_read_bytes/op", "bitmap_write_bytes/op", "explicit_copy_bytes/op",
	"parse_input_bytes/op", "candidates/op", "field_probes/op",
	"sort_comparisons/op", "rows_materialized/op",
	"unobserved_kernel_calls/op",
}

type movementSample [movementCounterCount]uint64

// Serialize captures. Do not run these diagnostics beside parallel workloads.
func captureMovement(tb testing.TB, run func()) movementSample {
	tb.Helper()
	movementCaptureMu.Lock()
	defer movementCaptureMu.Unlock()
	counts := new(movementCounts)
	if !activeMovement.CompareAndSwap(nil, counts) {
		tb.Fatal("movement collector is already installed")
	}
	defer activeMovement.Store(nil)
	run()
	var sample movementSample
	for i := range sample {
		sample[i] = counts[i].Load()
	}
	return sample
}

func movementSelection(batch *RecordBatch, threshold float64, bucketMax int64) (float64, error) {
	prices, err := batch.SelectFloat64("price", CompareGt, threshold)
	if err != nil {
		return 0, err
	}
	buckets, err := batch.SelectInt64("bucket", CompareLe, bucketMax)
	if err != nil {
		return 0, err
	}
	if err := prices.And(&buckets); err != nil {
		return 0, err
	}
	return batch.SumFloat64Selected("price", &prices)
}

func movementBatch(tb testing.TB, store *Store, rows int) *RecordBatch {
	tb.Helper()
	batch, err := store.GetColumnarBatch(context.Background(), "ticks",
		Query{OrganizationID: "org_1", Limit: max(rows, 1)}, []string{"price", "bucket"}, Fence{})
	if err != nil {
		tb.Fatal(err)
	}
	return batch
}

// Derive the oracle from fixture inputs, independently of columnar selection.
func movementExpected(rows int, threshold float64, bucketMax int64) (sum float64, selected, validPrice int, parseBytes int) {
	for i := range rows {
		parseBytes += len(strconv.Itoa(i % 16))
		if i%5 == 4 {
			continue
		}
		price := float64(i) * 1.5
		validPrice++
		parseBytes += len(strconv.FormatFloat(price, 'g', -1, 64))
		if price > threshold && int64(i%16) <= bucketMax {
			sum += price
			selected++
		}
	}
	return
}

func TestMovementFullPathBoundaries(t *testing.T) {
	for _, rows := range []int{1, 63, 64, 65, 1000} {
		t.Run(fmt.Sprint(rows), func(t *testing.T) {
			store := buildSelectFixtureStore(t, rows)
			threshold := float64(rows/2) * 1.5
			want, selected, validPrice, parseBytes := movementExpected(rows, threshold, 7)
			var sum float64
			sample := captureMovement(t, func() {
				var err error
				sum, err = movementSelection(movementBatch(t, store, rows), threshold, 7)
				if err != nil {
					t.Fatal(err)
				}
			})
			if sum != want {
				t.Fatalf("sum=%v, want %v", sum, want)
			}
			words := (rows + 63) / 64
			wantCounts := map[movementCounter]int{
				movementNumericRead: 8 * (2*rows + selected), movementNumericWrite: 8 * (rows + validPrice),
				movementBitmapRead:  8*(rows+validPrice) + 56*words,
				movementBitmapWrite: 8*(rows+validPrice) + 40*words,
				movementParseInput:  parseBytes, movementCandidates: rows,
				movementRowsMaterialized: rows, movementCopy: 0,
			}
			for counter, want := range wantCounts {
				if sample[counter] != uint64(want) {
					t.Errorf("%s=%d, want %d", movementMetricNames[counter], sample[counter], want)
				}
			}
			if sample[movementFieldProbes] == 0 {
				t.Error("construction did not report field probes")
			}
		})
	}
}

func TestMovementResidentSelection(t *testing.T) {
	store := buildSelectFixtureStore(t, 65)
	batch := movementBatch(t, store, 65)
	for _, test := range []struct {
		name      string
		threshold float64
		bucketMax int64
	}{
		{"none", math.Inf(1), 15},
		{"all_present", math.Inf(-1), 15},
		{"mixed", 48, 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			want, selected, _, _ := movementExpected(65, test.threshold, test.bucketMax)
			sample := captureMovement(t, func() {
				sum, err := movementSelection(batch, test.threshold, test.bucketMax)
				if err != nil || sum != want {
					t.Fatalf("sum=%v, want %v, err=%v", sum, want, err)
				}
			})
			if sample[movementNumericRead] != uint64(8*(130+selected)) || sample[movementNumericWrite] != 0 {
				t.Fatalf("resident numeric accesses=%v", sample)
			}
			if sample[movementRowsMaterialized] != 0 || sample[movementParseInput] != 0 || sample[movementCandidates] != 0 {
				t.Fatalf("resident selection reported construction work: %v", sample)
			}
		})
	}
}

func TestMovementEmptyAndMalformed(t *testing.T) {
	store := buildSelectFixtureStore(t, 0)
	sample := captureMovement(t, func() {
		batch := movementBatch(t, store, 0)
		if batch.Rows != 0 {
			t.Fatalf("empty batch contains %d rows", batch.Rows)
		}
		if _, err := movementSelection(batch, 0, 7); err == nil {
			t.Fatal("empty untyped columns unexpectedly became numeric")
		}
	})
	if sample[movementNumericRead] != 0 || sample[movementRowsMaterialized] != 0 {
		t.Fatalf("empty batch reported payload accesses: %v", sample)
	}
	for _, kind := range []database.RecordValueKind{database.RecordValueInt, database.RecordValueFloat} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			store := buildSelectFixtureStore(t, 0)
			record := database.DomainRecord{Domain: "signals", Collection: "ticks", OrganizationID: "org_1", RecordID: "bad",
				Data: database.RecordData{{Name: "price", Value: database.RecordValue{Kind: kind, Text: "bad"}}}}
			if _, err := store.BulkLoad(t.Context(), "ticks", []database.DomainRecord{record}); err != nil {
				t.Fatal(err)
			}
			sample := captureMovement(t, func() {
				_, err := store.GetColumnarBatch(t.Context(), "ticks", Query{OrganizationID: "org_1"}, []string{"price"}, Fence{})
				if err == nil || !strings.Contains(err.Error(), "failed to parse") {
					t.Fatalf("malformed numeric field err=%v", err)
				}
			})
			if sample[movementParseInput] != 3 || sample[movementNumericWrite] != 0 {
				t.Fatalf("malformed parse accounting=%v", sample)
			}
		})
	}
}

func TestMovementCopyRegression(t *testing.T) {
	src := []uint64{1, 2, 3}
	dst := make([]uint64, 2)
	sample := captureMovement(t, func() {
		if n := copyBitmapWords(dst, src); n != 2 {
			t.Fatalf("copied %d words, want 2", n)
		}
	})
	if dst[0] != 1 || dst[1] != 2 || sample[movementCopy] != 16 {
		t.Fatalf("copy result=%v, accounting=%v", dst, sample)
	}
	if sample[movementBitmapRead] != 16 || sample[movementBitmapWrite] != 16 {
		t.Fatalf("copy accesses=%v", sample)
	}
	// A repeated copy into existing storage must exceed the original byte ceiling.
	regressed := captureMovement(t, func() {
		copyBitmapWords(dst, src)
		copyBitmapWords(dst, src)
	})
	if regressed[movementCopy] <= sample[movementCopy] {
		t.Fatal("an extra copy bypassed the movement ceiling")
	}
	noCopy := captureMovement(t, func() { copyBitmapWords(nil, src) })
	if noCopy != (movementSample{}) {
		t.Fatalf("empty destination reported movement: %v", noCopy)
	}
}

func TestMovementReadBoundaries(t *testing.T) {
	store := buildSelectFixtureStore(t, 65)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, test := range []struct {
		name  string
		ctx   context.Context
		org   string
		fence Fence
		want  error
	}{
		{"canceled", canceled, "org_1", Fence{}, context.Canceled},
		{"missing_org", t.Context(), "", Fence{}, ErrInvalidEvent},
		{"fence", t.Context(), "org_1", Fence{MinEpoch: math.MaxUint64}, ErrFenceNotSatisfied},
		{"other_org", t.Context(), "org_other", Fence{}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			sample := captureMovement(t, func() {
				batch, err := store.GetColumnarBatch(test.ctx, "ticks", Query{OrganizationID: test.org}, []string{"price", "bucket"}, test.fence)
				if !errors.Is(err, test.want) {
					t.Fatalf("err=%v, want %v", err, test.want)
				}
				if err == nil && batch.Rows != 0 {
					t.Fatalf("other tenant received %d rows", batch.Rows)
				}
			})
			if sample != (movementSample{}) {
				t.Fatalf("rejected or empty scope accessed payloads: %v", sample)
			}
		})
	}
}

func TestMovementAlternateExecutorIsUnobserved(t *testing.T) {
	store := buildSelectFixtureStore(t, 65)
	batch := movementBatch(t, store, 65)
	hook := BitmapBinaryHook(func(op BitmapOp, dst, src []uint64) bool {
		if op != BitmapAnd {
			return false
		}
		for i := range dst {
			dst[i] &= src[i]
		}
		return true
	})
	SetBitmapBinaryHook(&hook)
	defer ClearBitmapDispatchHooks()
	sample := captureMovement(t, func() {
		want, _, _, _ := movementExpected(65, 48, 7)
		sum, err := movementSelection(batch, 48, 7)
		if err != nil || sum != want {
			t.Fatalf("alternate sum=%v, want %v, err=%v", sum, want, err)
		}
	})
	if sample[movementUnobservedKernels] != 3 {
		t.Fatalf("alternate executor escaped the coverage guard: %v", sample)
	}
}

func TestMovementDecliningExecutorIsUnobserved(t *testing.T) {
	store := buildSelectFixtureStore(t, 65)
	batch := movementBatch(t, store, 65)
	var scratch [2]uint64
	binaryHook := BitmapBinaryHook(func(_ BitmapOp, _, src []uint64) bool {
		copy(scratch[:], src)
		return false
	})
	countHook := BitmapCountHook(func(words []uint64) (int, bool) {
		copy(scratch[:], words)
		return 0, false
	})
	SetBitmapBinaryHook(&binaryHook)
	SetBitmapCountHook(&countHook)
	defer ClearBitmapDispatchHooks()
	sample := captureMovement(t, func() {
		want, _, _, _ := movementExpected(65, 48, 7)
		sum, err := movementSelection(batch, 48, 7)
		if err != nil || sum != want {
			t.Fatalf("fallback sum=%v, want %v, err=%v", sum, want, err)
		}
		if got := popcountWords([]uint64{3, 1}); got != 3 {
			t.Fatalf("fallback count=%d, want 3", got)
		}
	})
	if sample[movementUnobservedKernels] != 4 || scratch != [2]uint64{3, 1} {
		t.Fatalf("declined work escaped accounting: sample=%v scratch=%v", sample, scratch)
	}
}

func BenchmarkMovementHermesFullPath10K(b *testing.B) {
	benchmarkMovementHermes(b, false)
}

func BenchmarkMovementHermesResident10K(b *testing.B) {
	benchmarkMovementHermes(b, true)
}

func benchmarkMovementHermes(b *testing.B, resident bool) {
	store := buildSelectFixtureStore(b, 10000)
	var batch *RecordBatch
	if resident {
		batch = movementBatch(b, store, 10000)
	}
	want, _, _, _ := movementExpected(10000, 7500, 7)
	b.ReportAllocs()
	sample := captureMovement(b, func() {
		for b.Loop() {
			if !resident {
				batch = movementBatch(b, store, 10000)
			}
			sum, err := movementSelection(batch, 7500, 7)
			if err != nil || sum != want {
				b.Fatalf("sum=%v, want %v, err=%v", sum, want, err)
			}
		}
	})
	for counter, amount := range sample {
		b.ReportMetric(float64(amount)/float64(b.N), movementMetricNames[counter])
	}
}
