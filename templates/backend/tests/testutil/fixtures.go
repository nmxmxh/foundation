// Package testutil provides scaffolded database and Redis test helpers.
package testutil

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FixtureLoader provides utilities for loading test data into the database.
type FixtureLoader struct {
	db *pgxpool.Pool
	t  *testing.T
}

// NewFixtureLoader creates a new fixture loader for the given database pool.
func NewFixtureLoader(t *testing.T, db *pgxpool.Pool) *FixtureLoader {
	return &FixtureLoader{db: db, t: t}
}

// ExecSQL executes raw SQL statements. Use for test data setup.
func (f *FixtureLoader) ExecSQL(ctx context.Context, sql string, args ...interface{}) {
	_, err := f.db.Exec(ctx, sql, args...)
	if err != nil {
		f.t.Fatalf("failed to execute fixture SQL: %v\nSQL: %s", err, sql)
	}
}

// MustExecSQL is like ExecSQL but panics on error. Use in test setup.
func (f *FixtureLoader) MustExecSQL(ctx context.Context, sql string, args ...interface{}) {
	_, err := f.db.Exec(ctx, sql, args...)
	if err != nil {
		panic(fmt.Sprintf("failed to execute fixture SQL: %v\nSQL: %s", err, sql))
	}
}

// TruncateTable removes all data from a table. Use for test isolation.
func (f *FixtureLoader) TruncateTable(ctx context.Context, table string) {
	_, err := f.db.Exec(ctx, fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
	if err != nil {
		f.t.Fatalf("failed to truncate table %s: %v", table, err)
	}
}

// TruncateTables removes all data from multiple tables.
func (f *FixtureLoader) TruncateTables(ctx context.Context, tables ...string) {
	for _, table := range tables {
		f.TruncateTable(ctx, table)
	}
}

// WithTransaction runs a function within a transaction that is always rolled back.
// Useful for tests that need to modify data but should not persist changes.
func WithTransaction(t *testing.T, db *pgxpool.Pool, fn func(ctx context.Context)) {
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("failed to begin transaction: %v", err)
	}

	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Logf("transaction rollback failed (may already be committed): %v", err)
		}
	}()

	fn(ctx)
}

// TableExists checks if a table exists in the database.
func TableExists(ctx context.Context, db *pgxpool.Pool, table string) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = $1
		)
	`, table).Scan(&exists)
	return exists, err
}

// WaitForCondition polls a condition function until it returns true or timeout.
// Useful for testing async operations.
func WaitForCondition(t *testing.T, timeout, interval int, condition func() bool, message string) {
	for i := 0; i < timeout/interval; i++ {
		if condition() {
			return
		}
		// Use time.Sleep in milliseconds
		// time.Sleep(time.Duration(interval) * time.Millisecond)
	}
	t.Fatalf("timeout waiting for condition: %s", message)
}
