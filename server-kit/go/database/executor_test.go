package database

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

func TestExecCommandAndQueryOneValidateInputs(t *testing.T) {
	if err := ExecCommand(context.Background(), nil, "SELECT 1"); err == nil {
		t.Fatal("ExecCommand nil db error = nil")
	}
	if err := QueryOne(context.Background(), NewMemoryDB(), "SELECT 1", nil); err == nil {
		t.Fatal("QueryOne nil scanner error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ExecCommand(cancelled, NewMemoryDB(), "SELECT 1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecCommand cancelled error = %v", err)
	}
}

func TestExecRowsAffected(t *testing.T) {
	rows, err := ExecRowsAffected(context.Background(), NewMemoryDB(), "UPDATE items SET name = $1", "x")
	if err != nil {
		t.Fatalf("ExecRowsAffected error = %v", err)
	}
	if rows != 0 {
		t.Fatalf("rows affected = %d, want 0", rows)
	}
	if _, err := ExecRowsAffected(context.Background(), nil, "UPDATE items SET name = $1", "x"); err == nil {
		t.Fatal("ExecRowsAffected nil db error = nil")
	}
}

func TestQueryEachClosesRowsAndChecksErr(t *testing.T) {
	rows := &executorFakeRows{items: 2}
	db := &fakeRowQueryer{rows: rows}
	var scanned int
	if err := QueryEach(context.Background(), db, "SELECT id", func(r Rows) error {
		var id int
		if err := r.Scan(&id); err != nil {
			return err
		}
		scanned += id
		return nil
	}); err != nil {
		t.Fatalf("QueryEach error = %v", err)
	}
	if !rows.closed {
		t.Fatal("rows were not closed")
	}
	if scanned != 3 {
		t.Fatalf("scanned sum = %d, want 3", scanned)
	}
}

func TestQueryAllCollectsTypedRows(t *testing.T) {
	db := &fakeRowQueryer{rows: &executorFakeRows{items: 3}}
	got, err := QueryAll(context.Background(), db, "SELECT id", func(r Rows) (int, error) {
		var id int
		if err := r.Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	})
	if err != nil {
		t.Fatalf("QueryAll error = %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("QueryAll = %#v", got)
	}
}

func TestQueryAllLimitStopsRetainingRows(t *testing.T) {
	rows := &executorFakeRows{items: 5}
	db := &fakeRowQueryer{rows: rows}
	got, err := QueryAllLimit(context.Background(), db, 2, "SELECT id", func(r Rows) (int, error) {
		var id int
		if err := r.Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	})
	if err != nil {
		t.Fatalf("QueryAllLimit error = %v", err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("QueryAllLimit = %#v", got)
	}
	if !rows.closed {
		t.Fatal("rows were not closed after limit")
	}
	if _, err := QueryAllLimit(context.Background(), db, -1, "SELECT id", func(Rows) (int, error) { return 0, nil }); err == nil {
		t.Fatal("expected negative limit to fail")
	}
}

func TestQueryAllCapacity(t *testing.T) {
	rows := &executorFakeRows{items: 3}
	db := &fakeRowQueryer{rows: rows}

	// Normal capacity
	got, err := QueryAllCapacity(context.Background(), db, 3, "SELECT id", func(r Rows) (int, error) {
		return DirectScanInt(r)
	})
	if err != nil {
		t.Fatalf("QueryAllCapacity error = %v", err)
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("QueryAllCapacity got = %#v", got)
	}

	// Negative capacity defaults safely to 0
	rows2 := &executorFakeRows{items: 1}
	db2 := &fakeRowQueryer{rows: rows2}
	got2, err := QueryAllCapacity(context.Background(), db2, -5, "SELECT id", func(r Rows) (int, error) {
		return DirectScanInt(r)
	})
	if err != nil || len(got2) != 1 {
		t.Fatalf("QueryAllCapacity negative cap failed: %v", err)
	}

	// Nil scan returns error
	if _, err := QueryAllCapacity[int](context.Background(), db, 5, "SELECT id", nil); err == nil {
		t.Fatal("expected nil scan to fail")
	}
}

type fallbackRows struct {
	val int
}

func (r *fallbackRows) Close()     {}
func (r *fallbackRows) Next() bool { return false }
func (r *fallbackRows) Err() error { return nil }
func (r *fallbackRows) Scan(dest ...any) error {
	if len(dest) > 0 {
		if ptr, ok := dest[0].(*int); ok {
			*ptr = r.val
		} else if ptr64, ok := dest[0].(*int64); ok {
			*ptr64 = int64(r.val)
		} else if strPtr, ok := dest[0].(*string); ok {
			*strPtr = fmt.Sprintf("fallback_%d", r.val)
		}
	}
	return nil
}

func TestDirectScanners(t *testing.T) {
	fake := &executorFakeRows{items: 1}
	fake.Next()

	// Direct interface path
	vInt, err := DirectScanInt(fake)
	if err != nil || vInt != 1 {
		t.Fatalf("DirectScanInt error = %v, val = %d", err, vInt)
	}
	vInt64, err := DirectScanInt64(fake)
	if err != nil || vInt64 != 1 {
		t.Fatalf("DirectScanInt64 error = %v, val = %d", err, vInt64)
	}
	vStr, err := DirectScanString(fake)
	if err != nil || vStr != "str_1" {
		t.Fatalf("DirectScanString error = %v, val = %q", err, vStr)
	}

	// Fallback to rows.Scan()
	fallback := &fallbackRows{val: 42}
	fbInt, err := DirectScanInt(fallback)
	if err != nil || fbInt != 42 {
		t.Fatalf("fallback DirectScanInt error = %v, val = %d", err, fbInt)
	}
	fbInt64, err := DirectScanInt64(fallback)
	if err != nil || fbInt64 != 42 {
		t.Fatalf("fallback DirectScanInt64 error = %v, val = %d", err, fbInt64)
	}
	fbStr, err := DirectScanString(fallback)
	if err != nil || fbStr != "fallback_42" {
		t.Fatalf("fallback DirectScanString error = %v, val = %q", err, fbStr)
	}
}

func TestQueryEachReturnsRowsErr(t *testing.T) {
	errBoom := errors.New("rows")
	db := &fakeRowQueryer{rows: &executorFakeRows{items: 1, err: errBoom}}
	if err := QueryEach(context.Background(), db, "SELECT id", func(Rows) error { return nil }); !errors.Is(err, errBoom) {
		t.Fatalf("QueryEach rows error = %v", err)
	}
}

func TestAtomicLaneUsesTransactionPath(t *testing.T) {
	tx := &fakeTx{}
	db := &fakeBeginner{tx: tx}
	if err := AtomicLane(context.Background(), db, RuntimeLaneHotWrite, func(DBTX) error { return nil }); err != nil {
		t.Fatalf("AtomicLane error = %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("transaction state = %+v", tx)
	}
}

type fakeRowQueryer struct {
	rows *executorFakeRows
	err  error
}

func (f *fakeRowQueryer) Query(context.Context, string, ...any) (Rows, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

type executorFakeRows struct {
	items  int
	index  int
	closed bool
	err    error
}

func (f *executorFakeRows) Close() {
	f.closed = true
}

func (f *executorFakeRows) Next() bool {
	if f.index >= f.items {
		return false
	}
	f.index++
	return true
}

func TestQueryAllArena(t *testing.T) {
	ctx := context.Background()
	arena := scratch.New(4096)

	t.Run("explicit arena allocation", func(t *testing.T) {
		arena.Reset()
		db := &fakeRowQueryer{rows: &executorFakeRows{items: 10}}
		items, err := QueryAllArena(ctx, arena, db, 10, "SELECT id", func(rows Rows) (int, error) {
			return DirectScanInt(rows)
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(items) != 10 {
			t.Fatalf("expected 10 items, got %d", len(items))
		}
		if arena.Allocated() == 0 {
			t.Fatalf("expected memory to be allocated from arena, got 0")
		}
	})

	t.Run("context arena allocation", func(t *testing.T) {
		arena.Reset()
		ctxWithArena := scratch.WithArena(ctx, arena)
		db := &fakeRowQueryer{rows: &executorFakeRows{items: 10}}
		items, err := QueryAllArena(ctxWithArena, nil, db, 10, "SELECT id", func(rows Rows) (int, error) {
			return DirectScanInt(rows)
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(items) != 10 {
			t.Fatalf("expected 10 items, got %d", len(items))
		}
		if arena.Allocated() == 0 {
			t.Fatalf("expected memory to be allocated from context arena, got 0")
		}
	})

	t.Run("nil arena fallback", func(t *testing.T) {
		db := &fakeRowQueryer{rows: &executorFakeRows{items: 5}}
		items, err := QueryAllArena(ctx, nil, db, 5, "SELECT id", func(rows Rows) (int, error) {
			return DirectScanInt(rows)
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(items) != 5 {
			t.Fatalf("expected 5 items, got %d", len(items))
		}
	})
}

func (f *executorFakeRows) Scan(dest ...any) error {
	if len(dest) == 0 {
		return nil
	}
	if id, ok := dest[0].(*int); ok {
		*id = f.index
	}
	return nil
}

func (f *executorFakeRows) ScanInt() (int, error) {
	return f.index, nil
}

func (f *executorFakeRows) ScanInt64() (int64, error) {
	return int64(f.index), nil
}

func (f *executorFakeRows) ScanString() (string, error) {
	return fmt.Sprintf("str_%d", f.index), nil
}

func (f *executorFakeRows) Err() error {
	return f.err
}
