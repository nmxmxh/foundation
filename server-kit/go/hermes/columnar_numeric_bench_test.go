package hermes

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkHermesNumericValidityFullPath(b *testing.B) {
	for _, rows := range []int{1, 64, 65, 1024, 10000} {
		b.Run(fmt.Sprint(rows), func(b *testing.B) {
			store := buildSelectFixtureStore(b, rows)
			threshold := float64(rows) * 0.75
			var expected float64
			for row := range rows {
				price := float64(row) * 1.5
				if row%5 != 4 && price > threshold && row%16 <= 7 {
					expected += price
				}
			}
			query := Query{OrganizationID: "org_1", Limit: rows}
			fields := []string{"price", "bucket"}
			b.ReportAllocs()
			for b.Loop() {
				batch, err := store.GetColumnarBatch(context.Background(), "ticks", query, fields, Fence{})
				if err != nil {
					b.Fatal(err)
				}
				if got := numericValiditySelectedSum(b, batch, threshold); got != expected {
					b.Fatalf("sum=%v, want %v", got, expected)
				}
			}
		})
	}
}

func numericValiditySelectedSum(tb testing.TB, batch *RecordBatch, threshold float64) float64 {
	tb.Helper()
	prices, err := batch.SelectFloat64("price", CompareGt, threshold)
	if err != nil {
		tb.Fatal(err)
	}
	buckets, err := batch.SelectInt64("bucket", CompareLe, 7)
	if err != nil {
		tb.Fatal(err)
	}
	if err := prices.And(&buckets); err != nil {
		tb.Fatal(err)
	}
	sum, err := batch.SumFloat64Selected("price", &prices)
	if err != nil {
		tb.Fatal(err)
	}
	return sum
}
