package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
)

// SQLStore is the minimal pgx-compatible executor legacy repositories need
// while they migrate toward DBTX, RowQueryer, ResultExecutor, and AtomicLane.
type SQLStore interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ExecutorStore is the repository-facing database surface for SQL-backed app
// code. It is intentionally the composition of Foundation executor interfaces.
type ExecutorStore interface {
	DBTX
	RowQueryer
	ResultExecutor
	TxBeginner
}

// WrapSQLStore adapts pgxpool, pgxmock, and pgx transactions to Foundation's
// optimized executor helper interfaces.
func WrapSQLStore(store SQLStore) ExecutorStore {
	if store == nil {
		return nil
	}
	return sqlStoreExecutor{store: store}
}

type sqlStoreExecutor struct {
	store SQLStore
}

func (s sqlStoreExecutor) Exec(ctx context.Context, query string, args ...any) error {
	_, err := s.store.Exec(ctx, query, args...)
	return err
}

func (s sqlStoreExecutor) ExecResult(ctx context.Context, query string, args ...any) (CommandResult, error) {
	tag, err := s.store.Exec(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return commandResult{rowsAffected: tag.RowsAffected()}, nil
}

func (s sqlStoreExecutor) QueryRow(ctx context.Context, query string, args ...any) RowScanner {
	return s.store.QueryRow(ctx, query, args...)
}

func (s sqlStoreExecutor) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	return s.store.Query(ctx, query, args...)
}

func (s sqlStoreExecutor) BeginTx(ctx context.Context) (Tx, error) {
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return sqlTxExecutor{tx: tx}, nil
}

type sqlTxExecutor struct {
	tx pgx.Tx
}

func (s sqlTxExecutor) Exec(ctx context.Context, query string, args ...any) error {
	_, err := s.tx.Exec(ctx, query, args...)
	return err
}

func (s sqlTxExecutor) ExecResult(ctx context.Context, query string, args ...any) (CommandResult, error) {
	tag, err := s.tx.Exec(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return commandResult{rowsAffected: tag.RowsAffected()}, nil
}

func (s sqlTxExecutor) QueryRow(ctx context.Context, query string, args ...any) RowScanner {
	return s.tx.QueryRow(ctx, query, args...)
}

func (s sqlTxExecutor) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	return s.tx.Query(ctx, query, args...)
}

func (s sqlTxExecutor) Commit(ctx context.Context) error {
	return s.tx.Commit(ctx)
}

func (s sqlTxExecutor) Rollback(ctx context.Context) error {
	return s.tx.Rollback(ctx)
}

// QuerySQLOne scans a single pgx-compatible row. It is the migration bridge for
// application repositories that still use pgxpool or pgxmock directly.
func QuerySQLOne(ctx context.Context, db SQLStore, query string, scan func(RowScanner) error, args ...any) error {
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

// QuerySQLEach streams pgx-compatible rows while centralizing close/error
// handling for legacy repositories.
func QuerySQLEach(ctx context.Context, db SQLStore, query string, scan func(Rows) error, args ...any) error {
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

// QuerySQLAll collects a bounded pgx-compatible result set into a typed slice.
func QuerySQLAll[T any](ctx context.Context, db SQLStore, query string, scan func(Rows) (T, error), args ...any) ([]T, error) {
	return QuerySQLAllLimit(ctx, db, 0, query, scan, args...)
}

// QuerySQLAllLimit collects at most limit pgx-compatible rows. SQL should still
// include LIMIT; this is a retention guardrail for repository code.
func QuerySQLAllLimit[T any](ctx context.Context, db SQLStore, limit int, query string, scan func(Rows) (T, error), args ...any) ([]T, error) {
	if scan == nil {
		return nil, errors.New("row scanner function is required")
	}
	if limit < 0 {
		return nil, fmt.Errorf("query row limit must be non-negative: %d", limit)
	}
	capacity := max(limit, 0)
	items := make([]T, 0, capacity)
	err := QuerySQLEach(ctx, db, query, func(rows Rows) error {
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

// ExecSQLRowsAffected runs a pgx-compatible command and returns rows affected.
func ExecSQLRowsAffected(ctx context.Context, db SQLStore, query string, args ...any) (int64, error) {
	if db == nil {
		return 0, errors.New("database is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	result, err := db.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

// MarshalJSON preserves JSONB identity through database round-trips.
// Nil values are stored as an empty object so JSONB columns never receive null
// by accident.
func MarshalJSON(value any) ([]byte, error) {
	if value == nil {
		return []byte("{}"), nil
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	return raw, nil
}

// UnmarshalJSON decodes JSONB payloads and surfaces corruption instead of
// silently dropping state.
func UnmarshalJSON(raw []byte, dest any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("unmarshal json: %w", err)
	}
	return nil
}

// MarshalJSONB converts runtime payloads into JSONB bytes and returns a typed
// validation error when the caller supplied data cannot be represented as JSON.
func MarshalJSONB(value any, code, message string) ([]byte, error) {
	raw, err := MarshalJSON(value)
	if err != nil {
		return nil, domainerr.New(domainerr.KindValidation, code, message, err)
	}
	return raw, nil
}

// UnmarshalJSONB decodes JSONB bytes and returns a typed internal error for
// database corruption or incompatible persisted payloads.
func UnmarshalJSONB(raw []byte, dest any, code, message string) error {
	if err := UnmarshalJSON(raw, dest); err != nil {
		return domainerr.New(domainerr.KindInternal, code, message, err)
	}
	return nil
}

// NormalizePageBounds applies deterministic database pagination bounds.
func NormalizePageBounds(limit, offset, defaultLimit, maxLimit int) (int, int) {
	if defaultLimit <= 0 {
		defaultLimit = 20
	}
	if maxLimit <= 0 || maxLimit < defaultLimit {
		maxLimit = defaultLimit
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
