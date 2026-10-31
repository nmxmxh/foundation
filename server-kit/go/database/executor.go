package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

// ExecCommand runs a bounded command through the minimal DBTX contract.
func ExecCommand(ctx context.Context, db DBTX, query string, args ...any) error {
	// Definition: command lane. Best for INSERT/UPDATE/DELETE statements where
	// callers do not need a returned row count. pgx will still use its statement
	// cache underneath for repeated SQL on the Postgres adapter.
	if db == nil {
		return errors.New("database is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return db.Exec(ctx, query, args...)
}

// ExecCommandResult runs a command and returns driver-neutral metadata.
func ExecCommandResult(ctx context.Context, db ResultExecutor, query string, args ...any) (CommandResult, error) {
	// Definition: command-result lane. Best for UPDATE/DELETE paths that must
	// preserve strict not-found behavior through rows affected without exposing
	// pgconn.CommandTag to application repositories.
	if db == nil {
		return nil, errors.New("result executor is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return db.ExecResult(ctx, query, args...)
}

// ExecRowsAffected runs a command and returns only rows affected.
func ExecRowsAffected(ctx context.Context, db ResultExecutor, query string, args ...any) (int64, error) {
	result, err := ExecCommandResult(ctx, db, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// QueryOne scans a single row through the minimal DBTX contract.
func QueryOne(ctx context.Context, db DBTX, query string, scan func(RowScanner) error, args ...any) error {
	// Definition: single-row lane. Best for primary-key lookups and mutations
	// with RETURNING. The caller owns the typed scan so no map allocation is
	// introduced by the helper.
	if db == nil {
		return errors.New("database is required")
	}
	if scan == nil {
		return errors.New("row scanner function is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return scan(db.QueryRow(ctx, query, args...))
}

// QueryEach streams rows through a caller-owned scanner while ensuring rows are
// closed and rows.Err is checked exactly once.
func QueryEach(ctx context.Context, db RowQueryer, query string, scan func(Rows) error, args ...any) error {
	// Definition: streaming read lane. Best for bounded reads where the caller
	// wants typed scanning and may stop early or process incrementally. This is
	// the closest Foundation abstraction to raw pgx row iteration.
	if db == nil {
		return errors.New("row queryer is required")
	}
	if scan == nil {
		return errors.New("row scanner function is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// QueryAll collects streamed rows into a typed slice. Use QueryEach for very
// large result sets where retaining the entire slice is not appropriate.
func QueryAll[T any](ctx context.Context, db RowQueryer, query string, scan func(Rows) (T, error), args ...any) ([]T, error) {
	// Definition: bounded list lane. Best for first-page reads, dashboards, and
	// small result sets. It keeps scans typed while centralizing row closure and
	// error checking.
	return QueryAllLimit(ctx, db, 0, query, scan, args...)
}

// QueryAllLimit collects at most limit rows into a typed slice. The SQL should
// still push LIMIT to the database boundary; this helper is the retention
// guardrail for callers that stream from a broad source or stop early.
func QueryAllLimit[T any](ctx context.Context, db RowQueryer, limit int, query string, scan func(Rows) (T, error), args ...any) ([]T, error) {
	if scan == nil {
		return nil, errors.New("row scanner function is required")
	}
	if limit < 0 {
		return nil, fmt.Errorf("query row limit must be non-negative: %d", limit)
	}
	capacity := max(limit, 0)
	items := make([]T, 0, capacity)
	err := QueryEach(ctx, db, query, func(rows Rows) error {
		if limit > 0 && len(items) >= limit {
			return ErrQueryLimitReached
		}
		item, err := scan(rows)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	}, args...)
	if err != nil && !errors.Is(err, ErrQueryLimitReached) {
		return nil, err
	}
	return items, nil
}

// QueryAllCapacity collects streamed rows into a typed slice pre-allocated with capacity.
// This avoids geometric slice reallocations when the caller knows the approximate row count.
func QueryAllCapacity[T any](ctx context.Context, db RowQueryer, capacity int, query string, scan func(Rows) (T, error), args ...any) ([]T, error) {
	if scan == nil {
		return nil, errors.New("row scanner function is required")
	}
	if capacity < 0 {
		capacity = 0
	}
	items := make([]T, 0, capacity)
	err := QueryEach(ctx, db, query, func(rows Rows) error {
		item, err := scan(rows)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}
	return items, nil
}

// QueryAllArena collects streamed rows into a typed slice allocated inside a scratch arena.
// If arena is nil, it inspects ctx via scratch.FromContext(ctx).
// Only pointer-free T uses the arena. Other T (strings, slices, pointers) use the heap.
// WARNING: an arena-backed result is valid only until the arena is reset.
func QueryAllArena[T any](ctx context.Context, arena *scratch.Arena, db RowQueryer, capacity int, query string, scan func(Rows) (T, error), args ...any) ([]T, error) {
	if scan == nil {
		return nil, errors.New("row scanner function is required")
	}
	if capacity < 0 {
		capacity = 0
	}
	if arena == nil && ctx != nil {
		arena = scratch.FromContext(ctx)
	}

	var items []T
	if arena != nil && capacity > 0 {
		buf, err := scratch.AllocSlice[T](arena, capacity)
		if err == nil {
			items = buf[:0]
		}
	}
	if items == nil {
		items = make([]T, 0, capacity)
	}

	err := QueryEach(ctx, db, query, func(rows Rows) error {
		item, err := scan(rows)
		if err != nil {
			return err
		}
		items = append(items, item)
		return nil
	}, args...)
	if err != nil {
		return nil, err
	}
	return items, nil
}

// IntScanner extracts a single int from the current row without interface boxing allocations.
type IntScanner interface {
	ScanInt() (int, error)
}

// Int64Scanner extracts a single int64 from the current row without interface boxing allocations.
type Int64Scanner interface {
	ScanInt64() (int64, error)
}

// StringScanner extracts a single string from the current row without interface boxing allocations.
type StringScanner interface {
	ScanString() (string, error)
}

// DirectScanInt scans a single int value avoiding variadic slice and interface escape allocations.
func DirectScanInt(rows Rows) (int, error) {
	if s, ok := rows.(IntScanner); ok {
		return s.ScanInt()
	}
	var val int
	if err := rows.Scan(&val); err != nil {
		return 0, err
	}
	return val, nil
}

// DirectScanInt64 scans a single int64 value avoiding variadic slice and interface escape allocations.
func DirectScanInt64(rows Rows) (int64, error) {
	if s, ok := rows.(Int64Scanner); ok {
		return s.ScanInt64()
	}
	var val int64
	if err := rows.Scan(&val); err != nil {
		return 0, err
	}
	return val, nil
}

// DirectScanString scans a single string value avoiding variadic slice and interface escape allocations.
func DirectScanString(rows Rows) (string, error) {
	if s, ok := rows.(StringScanner); ok {
		return s.ScanString()
	}
	var val string
	if err := rows.Scan(&val); err != nil {
		return "", err
	}
	return val, nil
}

// AtomicLane runs a transaction under the timeout budget for the selected lane.
func AtomicLane(ctx context.Context, db DBTX, lane RuntimeLane, fn func(DBTX) error) error {
	// Definition: transaction lane. The closure must contain only database work:
	// no network calls, unbounded waits, or callbacks into user-controlled logic.
	// Postgres pools also apply statement, lock, and idle-transaction session
	// budgets; keeping the closure tight preserves those backpressure semantics.
	budgetCtx, cancel := QueryBudgetContext(ctx, DefaultPoolOptionsFor(lane))
	defer cancel()
	return Atomic(budgetCtx, db, fn)
}
