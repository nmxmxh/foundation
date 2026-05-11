package database

import (
	"context"
	"errors"
	"testing"
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

func (f *executorFakeRows) Scan(dest ...any) error {
	if len(dest) == 0 {
		return nil
	}
	if id, ok := dest[0].(*int); ok {
		*id = f.index
	}
	return nil
}

func (f *executorFakeRows) Err() error {
	return f.err
}
