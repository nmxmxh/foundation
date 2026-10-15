package hermes

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

type numericValidityOracle struct {
	qty                  int64
	price                float64
	qtyValid, priceValid bool
}

func numericValidityFixture(t *testing.T, rows int, pattern string) (*Store, []numericValidityOracle) {
	t.Helper()
	store := newTestStore(t, ProjectionSpec{Name: "numbers", Domain: "test", Collection: "numbers", MaxRecords: max(rows, 1)})
	records := make([]database.DomainRecord, rows)
	oracle := make([]numericValidityOracle, rows)
	for row := range records {
		item := numericValidityOracle{qty: int64(row) - 64, price: float64(row) / 4, qtyValid: true, priceValid: true}
		if row == 0 {
			item.price = math.Copysign(0, -1)
		}
		switch pattern {
		case "sparse":
			item.qtyValid, item.priceValid = row%67 == 0, row%67 == 0
		case "null_word":
			item.qtyValid, item.priceValid = row < 64 || row >= 128, row < 64 || row >= 128
		case "absent":
			item.qtyValid, item.priceValid = false, false
		case "mixed":
			item.qtyValid, item.priceValid = row%7 != 6, row%5 != 4
		}
		var data database.RecordData
		if item.qtyValid {
			value := database.IntValue(item.qty)
			if row >= 64 && row%3 == 0 {
				value = database.UintValue(uint64(row - 64))
			}
			data = append(data, database.RecordField{Name: "qty", Value: value})
		}
		if item.priceValid {
			data = append(data, database.RecordField{Name: "price", Value: database.FloatValue(item.price)})
		}
		if pattern == "mixed" && row%11 == 10 {
			data = database.RecordData{{Name: "qty", Value: database.FloatValue(3)}, {Name: "price", Value: database.IntValue(3)}}
			item.qtyValid, item.priceValid = false, false
		}
		if pattern == "mixed" && row%17 == 16 {
			data = database.RecordData{{Name: "qty", Value: database.RecordValue{}}, {Name: "price", Value: database.RecordValue{}}}
			item.qtyValid, item.priceValid = false, false
		}
		records[row] = database.DomainRecord{Domain: "test", Collection: "numbers", OrganizationID: "org_1",
			RecordID: fmt.Sprintf("row_%06d", row), UpdatedAt: time.Unix(int64(rows-row), 0), Data: data}
		oracle[row] = item
	}
	if _, err := store.BulkLoad(t.Context(), "numbers", records); err != nil {
		t.Fatal(err)
	}
	return store, oracle
}

func TestNumericValidityPublicPath(t *testing.T) {
	for _, rows := range []int{0, 1, 63, 64, 65, 127, 128, 129, 1024} {
		for _, pattern := range []string{"dense", "sparse", "null_word", "mixed", "absent"} {
			t.Run(fmt.Sprintf("%d/%s", rows, pattern), func(t *testing.T) {
				store, oracle := numericValidityFixture(t, rows, pattern)
				batch, err := store.GetColumnarBatch(t.Context(), "numbers", Query{OrganizationID: "org_1"},
					[]string{"record_id", " qty ", "price"}, Fence{})
				if err != nil || batch.Rows != rows {
					t.Fatalf("rows=%v error=%v", batch, err)
				}
				if rows == 0 || pattern == "absent" {
					for _, column := range batch.Columns[1:] {
						if column.Data.Type() != TypeString || column.Data.NullCount() != rows {
							t.Fatalf("absent column: type=%v nulls=%d", column.Data.Type(), column.Data.NullCount())
						}
					}
					return
				}
				assertNumericValidityBatch(t, batch, oracle)
			})
		}
	}
}

func assertNumericValidityBatch(t *testing.T, batch *RecordBatch, oracle []numericValidityOracle) {
	t.Helper()
	qty := batch.Columns[1].Data.(*Int64Vector)
	price := batch.Columns[2].Data.(*Float64Vector)
	ids := batch.Columns[0].Data.(*StringVector)
	qtyNulls, priceNulls := 0, 0
	var expected float64
	for row, item := range oracle {
		wantQty, wantPrice := int64(0), float64(0)
		if item.qtyValid {
			wantQty = item.qty
		} else {
			qtyNulls++
		}
		if item.priceValid {
			wantPrice = item.price
		} else {
			priceNulls++
		}
		if ids.ValueAt(row) != fmt.Sprintf("row_%06d", row) || qty.IsValid(row) != item.qtyValid || price.IsValid(row) != item.priceValid {
			t.Fatalf("row %d order or validity changed", row)
		}
		if qty.values[row] != wantQty || math.Float64bits(price.values[row]) != math.Float64bits(wantPrice) {
			t.Fatalf("row %d: qty=%d price=%v", row, qty.values[row], price.values[row])
		}
		if item.qtyValid && item.priceValid && item.qty >= 0 && item.price > 0 {
			expected += item.price
		}
	}
	if qty.NullCount() != qtyNulls || price.NullCount() != priceNulls {
		t.Fatal("null count changed")
	}
	for _, bits := range []*bitmap{&qty.validity.bitmap, &price.validity.bitmap} {
		if rem := batch.Rows & 63; rem != 0 && bits.words[len(bits.words)-1]>>uint(rem) != 0 {
			t.Fatal("tail contains phantom rows")
		}
	}
	selected, err := batch.SelectInt64(" qty ", CompareGe, 0)
	if err != nil {
		t.Fatal(err)
	}
	prices, err := batch.SelectFloat64("price", CompareGt, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := selected.And(&prices); err != nil {
		t.Fatal(err)
	}
	got, err := batch.SumFloat64Selected("price", &selected)
	if err != nil || got != expected {
		t.Fatalf("selected sum=%v, want %v; error=%v", got, expected, err)
	}
}

func TestNumericValidityMalformedAfterWord(t *testing.T) {
	for _, test := range []struct {
		name string
		kind database.RecordValueKind
		text string
		want error
	}{
		{"integer", database.RecordValueInt, "bad", strconv.ErrSyntax},
		{"unsigned_overflow", database.RecordValueUint, "18446744073709551615", strconv.ErrRange},
		{"float", database.RecordValueFloat, "bad", strconv.ErrSyntax},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(t, ProjectionSpec{Name: "numbers", Domain: "test", Collection: "numbers", MaxRecords: 65})
			records := make([]database.DomainRecord, 65)
			for row := range records {
				text := "1"
				if row == 64 {
					text = test.text
				}
				records[row] = database.DomainRecord{Domain: "test", Collection: "numbers", OrganizationID: "org_1",
					RecordID: fmt.Sprint(row), UpdatedAt: time.Unix(int64(65-row), 0),
					Data: database.RecordData{{Name: "value", Value: database.RecordValue{Kind: test.kind, Text: text}}}}
			}
			if _, err := store.BulkLoad(t.Context(), "numbers", records); err != nil {
				t.Fatal(err)
			}
			batch, err := store.GetColumnarBatch(t.Context(), "numbers", Query{OrganizationID: "org_1"}, []string{"value"}, Fence{})
			if batch != nil || !errors.Is(err, test.want) {
				t.Fatalf("batch=%v error=%v, want %v", batch, err, test.want)
			}
		})
	}
}

func TestNumericValidityBatchOwnership(t *testing.T) {
	store, oracle := numericValidityFixture(t, 65, "mixed")
	query, fields := Query{OrganizationID: "org_1"}, []string{"record_id", " qty ", "price"}
	first, err := store.GetColumnarBatch(t.Context(), "numbers", query, fields, Fence{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.GetColumnarBatch(t.Context(), "numbers", query, fields, Fence{})
	if err != nil {
		t.Fatal(err)
	}
	first.Columns[1].Data.Int64Values()[0] = 999
	first.Columns[2].Data.(*Float64Vector).validity.words[0] = 0
	assertNumericValidityBatch(t, second, oracle)
	third, err := store.GetColumnarBatch(t.Context(), "numbers", query, fields, Fence{})
	if err != nil {
		t.Fatal(err)
	}
	assertNumericValidityBatch(t, third, oracle)
}
