package database

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// CopyFromSource bulk-loads rows through PostgreSQL COPY. Use this for large
// append/import workloads where INSERT-per-row would waste round trips and
// parsing overhead. For generated rows, prefer pgx.CopyFromSlice to avoid
// buffering the entire input in memory.
func (db *PostgresDB) CopyFromSource(ctx context.Context, tablePath []string, columns []string, source pgx.CopyFromSource) (int64, error) {
	// Definition: bulk ingest lane. This intentionally stays Postgres/pgx
	// specific because COPY is a database capability, not a generic DBTX method.
	if db == nil || db.pool == nil {
		return 0, errors.New("postgres pool is nil")
	}
	if len(tablePath) == 0 {
		return 0, errors.New("copy table path is required")
	}
	if len(columns) == 0 {
		return 0, errors.New("copy columns are required")
	}
	if source == nil {
		return 0, errors.New("copy source is required")
	}
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "copy_from")
	if err != nil {
		return 0, err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	copied, err := conn.Conn().CopyFrom(queryCtx, pgx.Identifier(tablePath), columns, source)
	err = normalizePostgresOperationError(contextErr(queryCtx), err)
	recordDatabaseOperation("copy_from", start, err)
	return copied, err
}

// CopyFromRows bulk-loads an in-memory row slice through PostgreSQL COPY.
func (db *PostgresDB) CopyFromRows(ctx context.Context, tablePath []string, columns []string, rows [][]any) (int64, error) {
	return db.CopyFromSource(ctx, tablePath, columns, pgx.CopyFromRows(rows))
}

// SendBatch runs a pgx batch under the database query budget. Batch is best
// when several independent statements must cross the client/server boundary
// together, but the operation is not a true COPY workload.
func (db *PostgresDB) SendBatch(ctx context.Context, build func(*pgx.Batch), consume func(pgx.BatchResults) error) error {
	// Definition: round-trip amortization lane. Do not use it to hide business
	// transactions; wrap it in AtomicLane when the statements must commit as one.
	if db == nil || db.pool == nil {
		return errors.New("postgres pool is nil")
	}
	if build == nil {
		return errors.New("batch builder is required")
	}
	if consume == nil {
		return errors.New("batch consumer is required")
	}
	var batch pgx.Batch
	build(&batch)
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "send_batch")
	if err != nil {
		return err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	results := conn.Conn().SendBatch(queryCtx, &batch)
	defer results.Close()
	err = consume(results)
	err = normalizePostgresOperationError(contextErr(queryCtx), err)
	recordDatabaseOperation("send_batch", start, err)
	return err
}
