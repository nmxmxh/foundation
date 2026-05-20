package database

import (
	"context"
	"testing"
)

func BenchmarkQueryAllFakeRows100(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		db := &fakeRowQueryer{rows: &executorFakeRows{items: 100}}
		items, err := QueryAll(ctx, db, "SELECT id", func(rows Rows) (int, error) {
			var id int
			if err := rows.Scan(&id); err != nil {
				return 0, err
			}
			return id, nil
		})
		if err != nil {
			b.Fatal(err)
		}
		if len(items) != 100 {
			b.Fatalf("len(items) = %d", len(items))
		}
	}
}

func BenchmarkExecCommandMemoryDB(b *testing.B) {
	ctx := context.Background()
	db := NewMemoryDB()
	for i := 0; i < b.N; i++ {
		if err := ExecCommand(ctx, db, "UPDATE items SET value = $1", i); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkExecRowsAffectedFake(b *testing.B) {
	ctx := context.Background()
	db := fakeResultExecutor{rowsAffected: 1}
	for i := 0; i < b.N; i++ {
		rows, err := ExecRowsAffected(ctx, db, "UPDATE items SET value = $1", i)
		if err != nil {
			b.Fatal(err)
		}
		if rows != 1 {
			b.Fatalf("rows affected = %d", rows)
		}
	}
}

type fakeResultExecutor struct {
	rowsAffected int64
}

func (f fakeResultExecutor) ExecResult(context.Context, string, ...any) (CommandResult, error) {
	return commandResult(f), nil
}

func BenchmarkQueryEachFakeRows100(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		db := &fakeRowQueryer{rows: &executorFakeRows{items: 100}}
		var total int
		if err := QueryEach(ctx, db, "SELECT id", func(rows Rows) error {
			var id int
			if err := rows.Scan(&id); err != nil {
				return err
			}
			total += id
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		if total == 0 {
			b.Fatal("total was zero")
		}
	}
}
