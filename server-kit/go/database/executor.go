package database

import (
	"context"
	"errors"
	"fmt"
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

// AtomicLane runs a transaction under the timeout budget for the selected lane.
func AtomicLane(ctx context.Context, db DBTX, lane RuntimeLane, fn func(DBTX) error) error {
	// Definition: transaction lane. The closure must contain only database work:
	// no network calls, unbounded waits, or callbacks into user-controlled logic.
	budgetCtx, cancel := QueryBudgetContext(ctx, DefaultPoolOptionsFor(lane))
	defer cancel()
	return Atomic(budgetCtx, db, fn)
}
