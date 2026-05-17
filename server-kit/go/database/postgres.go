package database

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/observability"
)

const maxInt32Value = 2_147_483_647

const upsertRecordSQL = `
	WITH upsert AS (
		INSERT INTO governance_state_records (domain, collection_name, organization_id, record_id, data)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		ON CONFLICT (domain, collection_name, organization_id, record_id)
		DO UPDATE SET data = EXCLUDED.data, updated_at = NOW()
		WHERE governance_state_records.data IS DISTINCT FROM EXCLUDED.data
		RETURNING created_at, updated_at
	)
	SELECT created_at, updated_at FROM upsert
	UNION ALL
	SELECT created_at, updated_at
	FROM governance_state_records
	WHERE domain = $1
	  AND collection_name = $2
	  AND organization_id = $3
	  AND record_id = $4
	  AND NOT EXISTS (SELECT 1 FROM upsert)
	LIMIT 1
`

const upsertRecordJSONSQL = `
	WITH incoming AS (
		SELECT
			$1::text AS domain,
			$2::text AS collection_name,
			$3::text AS organization_id,
			$4::text AS record_id,
			CASE
				WHEN $6::text = '' THEN $5::jsonb
				ELSE $5::jsonb || jsonb_build_object('organization_id', $6::text)
			END AS data
	),
	upsert AS (
		INSERT INTO governance_state_records (domain, collection_name, organization_id, record_id, data)
		SELECT domain, collection_name, organization_id, record_id, data
		FROM incoming
		ON CONFLICT (domain, collection_name, organization_id, record_id)
		DO UPDATE SET data = EXCLUDED.data, updated_at = NOW()
		WHERE governance_state_records.data IS DISTINCT FROM EXCLUDED.data
		RETURNING created_at, updated_at
	)
	SELECT created_at, updated_at FROM upsert
	UNION ALL
	SELECT g.created_at, g.updated_at
	FROM governance_state_records g
	JOIN incoming i
	  ON g.domain = i.domain
	 AND g.collection_name = i.collection_name
	 AND g.organization_id = i.organization_id
	 AND g.record_id = i.record_id
	WHERE NOT EXISTS (SELECT 1 FROM upsert)
	LIMIT 1
`

const (
	defaultPostgresRecheckRowBudget = 1024
	maxPostgresRecheckRowBudget     = 10000
)

type StateListOptions struct {
	Limit             int
	RecheckRowBudget  int
	RequirePushdown   bool
	ProjectionColumns []string
}

type StateCountOptions struct {
	RecheckRowBudget int
	RequirePushdown  bool
}

// PostgresDB is a pgx-backed runtime adapter for DBTX + StateStore.
type PostgresDB struct {
	pool *pgxpool.Pool
	opts PoolOptions
}

// Pool exposes the underlying pgx pool for application code that still needs
// direct pgx access while the broader foundation migrates toward RuntimeStore.
func (db *PostgresDB) Pool() *pgxpool.Pool {
	if db == nil {
		return nil
	}
	return db.pool
}

type postgresTx struct {
	tx pgx.Tx
}

type postgresCommandResult struct {
	tag pgconn.CommandTag
}

func (r postgresCommandResult) RowsAffected() int64 {
	return r.tag.RowsAffected()
}

type budgetedRow struct {
	row     RowScanner
	cancel  context.CancelFunc
	release func()
	start   time.Time
}

type budgetedRows struct {
	rows    pgx.Rows
	cancel  context.CancelFunc
	release func()
	start   time.Time
}

func (r budgetedRow) Scan(dest ...any) error {
	defer r.cancel()
	if r.release != nil {
		defer r.release()
	}
	err := r.row.Scan(dest...)
	recordDatabaseOperation("query_row", r.start, err)
	return err
}

func (r budgetedRows) Close() {
	r.rows.Close()
	if r.release != nil {
		r.release()
	}
	r.cancel()
	recordDatabaseOperation("query", r.start, r.rows.Err())
}

func (r budgetedRows) Next() bool {
	return r.rows.Next()
}

func (r budgetedRows) Scan(dest ...any) error {
	return r.rows.Scan(dest...)
}

func (r budgetedRows) Err() error {
	return r.rows.Err()
}

func newPostgresDB(ctx context.Context, databaseURL string, opts PoolOptions) (*PostgresDB, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("database url is required for postgres driver")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	opts = normalizePoolOptions(opts)

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	ApplyPoolOptions(cfg, opts)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresDB{pool: pool, opts: opts}, nil
}

// ApplyPoolOptions applies Foundation pool sizing, cache, and timeout settings
// to a pgxpool.Config. It lets scaffolded workers use the same budget defaults
// as RuntimeStore without duplicating environment parsing or raw pgx tuning.
func ApplyPoolOptions(cfg *pgxpool.Config, opts PoolOptions) {
	if cfg == nil {
		return
	}
	opts = normalizePoolOptions(opts)
	cfg.MaxConns = clampInt32(opts.MaxConns)
	cfg.MinConns = clampInt32(opts.MinConns)
	cfg.HealthCheckPeriod = opts.HealthCheckPeriod
	cfg.ConnConfig.ConnectTimeout = opts.ConnectTimeout
	cfg.ConnConfig.StatementCacheCapacity = opts.StatementCacheCapacity
	cfg.ConnConfig.DescriptionCacheCapacity = opts.DescriptionCacheCapacity
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = make(map[string]string, 1)
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = fmt.Sprintf("%d", opts.QueryTimeout.Milliseconds())
}

// WrapPostgresPool projects an existing pgx pool into Foundation's RuntimeStore
// surface. This is the migration lane for scaffolded projects that still need
// the raw pool for River, health checks, or provider-specific hooks while new
// repositories move onto DBTX, RowQueryer, ResultExecutor, and StateStore.
func WrapPostgresPool(pool *pgxpool.Pool, options ...PoolOptions) *PostgresDB {
	opts := DefaultPoolOptions()
	if len(options) > 0 {
		opts = normalizePoolOptions(options[0])
	}
	return &PostgresDB{pool: pool, opts: opts}
}

func clampInt32(value int) int32 {
	if value < 0 {
		return 0
	}
	if value > maxInt32Value {
		return maxInt32Value
	}
	return int32(value)
}

func (db *PostgresDB) Close() {
	if db == nil || db.pool == nil {
		return
	}
	db.pool.Close()
}

func (db *PostgresDB) BeginTx(ctx context.Context) (Tx, error) {
	if db == nil || db.pool == nil {
		return nil, errors.New("postgres pool is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	acquireCtx, cancel := AcquireBudgetContext(ctx, db.opts)
	defer cancel()
	start := time.Now()
	tx, err := db.pool.BeginTx(acquireCtx, pgx.TxOptions{})
	if err != nil {
		err = normalizeAcquireError(acquireCtx.Err(), err)
		recordDatabaseOperation("begin_tx_acquire", start, err)
		db.recordPoolPressure()
		return nil, err
	}
	return &postgresTx{tx: tx}, nil
}

func (db *PostgresDB) Exec(ctx context.Context, query string, args ...any) error {
	if db == nil || db.pool == nil {
		return errors.New("postgres pool is nil")
	}
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "exec")
	if err != nil {
		return err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	_, err = conn.Exec(queryCtx, query, args...)
	recordDatabaseOperation("exec", start, err)
	return err
}

func (db *PostgresDB) ExecResult(ctx context.Context, query string, args ...any) (CommandResult, error) {
	if db == nil || db.pool == nil {
		return nil, errors.New("postgres pool is nil")
	}
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "exec_result")
	if err != nil {
		return nil, err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	tag, err := conn.Exec(queryCtx, query, args...)
	recordDatabaseOperation("exec_result", start, err)
	if err != nil {
		return nil, err
	}
	return postgresCommandResult{tag: tag}, nil
}

func (db *PostgresDB) QueryRow(ctx context.Context, query string, args ...any) RowScanner {
	if db == nil || db.pool == nil {
		return memoryRow{err: errors.New("postgres pool is nil")}
	}
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "query_row")
	if err != nil {
		return memoryRow{err: err}
	}
	return budgetedRow{row: conn.QueryRow(queryCtx, query, args...), cancel: cancel, release: conn.Release, start: start}
}

func (db *PostgresDB) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	if db == nil || db.pool == nil {
		return nil, errors.New("postgres pool is nil")
	}
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "query")
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(queryCtx, query, args...)
	if err != nil {
		conn.Release()
		cancel()
		recordDatabaseOperation("query", start, err)
		return nil, err
	}
	return budgetedRows{rows: rows, cancel: cancel, release: conn.Release, start: start}, nil
}

func (db *PostgresDB) QueryMaps(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if db == nil || db.pool == nil {
		return nil, errors.New("postgres pool is nil")
	}
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "query_maps")
	if err != nil {
		return nil, err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	rows, err := conn.Query(queryCtx, query, args...)
	if err != nil {
		recordDatabaseOperation("query_maps", start, err)
		return nil, err
	}
	defer rows.Close()
	result, err := scanToMaps(rows)
	recordDatabaseOperation("query_maps", start, err)
	return result, err
}

func (db *PostgresDB) Stats() StoreStats {
	if db == nil || db.pool == nil {
		return StoreStats{}
	}
	s := db.pool.Stat()
	recordDatabasePoolPressure(db.pool)
	return StoreStats{
		TotalConns:      s.TotalConns(),
		IdleConns:       s.IdleConns(),
		ActiveConns:     s.TotalConns() - s.IdleConns(),
		AcquireCount:    s.AcquireCount(),
		AcquireDuration: s.AcquireDuration(),
		MaxConns:        s.MaxConns(),
		ConstructedAt:   time.Now(), // Approx as pgxpool doesn't track this directly
	}
}

func (db *PostgresDB) recordPoolPressure() {
	if db == nil || db.pool == nil {
		return
	}
	recordDatabasePoolPressure(db.pool)
}

func (db *PostgresDB) acquireConn(ctx context.Context, operation string) (*pgxpool.Conn, context.Context, func(), time.Time, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	acquireCtx, acquireCancel := AcquireBudgetContext(ctx, db.opts)
	start := time.Now()
	conn, err := db.pool.Acquire(acquireCtx)
	acquireErr := acquireCtx.Err()
	acquireCancel()
	if err != nil {
		err = normalizeAcquireError(acquireErr, err)
		recordDatabaseOperation(operation+"_acquire", start, err)
		db.recordPoolPressure()
		return nil, nil, nil, start, err
	}
	queryCtx, queryCancel := QueryBudgetContext(ctx, db.opts)
	cancel := func() {
		queryCancel()
		db.recordPoolPressure()
	}
	return conn, queryCtx, cancel, start, nil
}

func normalizeAcquireError(ctxErr error, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctxErr, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrPoolAcquireTimeout, err)
	}
	return err
}

func recordDatabasePoolPressure(pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	s := pool.Stat()
	observability.Default().RecordDatabasePool(
		"postgres",
		s.TotalConns()-s.IdleConns(),
		s.IdleConns(),
		s.TotalConns(),
		s.MaxConns(),
		s.AcquireCount(),
		s.AcquireDuration(),
	)
}

func recordDatabaseOperation(operation string, start time.Time, err error) {
	state := "success"
	if err != nil {
		state = "error"
	}
	observability.Default().RecordDatabaseOperation(operation, state, time.Since(start))
}

func (tx *postgresTx) Exec(ctx context.Context, query string, args ...any) error {
	if tx == nil || tx.tx == nil {
		return errors.New("postgres tx is nil")
	}
	_, err := tx.tx.Exec(ctx, query, args...)
	return err
}

func (tx *postgresTx) ExecResult(ctx context.Context, query string, args ...any) (CommandResult, error) {
	if tx == nil || tx.tx == nil {
		return nil, errors.New("postgres tx is nil")
	}
	tag, err := tx.tx.Exec(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return postgresCommandResult{tag: tag}, nil
}

func (tx *postgresTx) QueryRow(ctx context.Context, query string, args ...any) RowScanner {
	if tx == nil || tx.tx == nil {
		return memoryRow{err: errors.New("postgres tx is nil")}
	}
	return tx.tx.QueryRow(ctx, query, args...)
}

func (tx *postgresTx) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	if tx == nil || tx.tx == nil {
		return nil, errors.New("postgres tx is nil")
	}
	return tx.tx.Query(ctx, query, args...)
}

func (tx *postgresTx) QueryMaps(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if tx == nil || tx.tx == nil {
		return nil, errors.New("postgres tx is nil")
	}
	rows, err := tx.tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanToMaps(rows)
}

func scanToMaps(rows pgx.Rows) ([]map[string]any, error) {
	fields := rows.FieldDescriptions()
	numFields := len(fields)
	items := make([]map[string]any, 0, 32)

	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}

		item := make(map[string]any, numFields)
		for i := range numFields {
			val := values[i]
			if b, ok := val.([]byte); ok {
				item[fields[i].Name] = string(b)
			} else {
				item[fields[i].Name] = val
			}
		}
		items = append(items, item)
	}

	return items, rows.Err()
}

func (tx *postgresTx) Commit(ctx context.Context) error {
	if tx == nil || tx.tx == nil {
		return errors.New("postgres tx is nil")
	}
	return tx.tx.Commit(ctx)
}

func (tx *postgresTx) Rollback(ctx context.Context) error {
	if tx == nil || tx.tx == nil {
		return nil
	}
	return tx.tx.Rollback(ctx)
}

func (db *PostgresDB) UpsertRecord(ctx context.Context, rec DomainRecord) (DomainRecord, error) {
	if db == nil || db.pool == nil {
		return DomainRecord{}, errors.New("postgres pool is nil")
	}
	if err := validateDomainRecord(&rec); err != nil {
		return DomainRecord{}, err
	}

	payload, err := json.Marshal(rec.Data)
	if err != nil {
		return DomainRecord{}, err
	}

	var (
		createdAt time.Time
		updatedAt time.Time
	)
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "upsert_record")
	if err != nil {
		return DomainRecord{}, err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	err = conn.QueryRow(queryCtx, upsertRecordSQL, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID, payload).Scan(&createdAt, &updatedAt)
	recordDatabaseOperation("upsert_record", start, err)
	if err != nil {
		return DomainRecord{}, err
	}

	rec.CreatedAt = createdAt.UTC()
	rec.UpdatedAt = updatedAt.UTC()
	return rec, nil
}

func (db *PostgresDB) UpsertRecordJSON(ctx context.Context, rec RawDomainRecord) (RawDomainRecord, error) {
	if db == nil || db.pool == nil {
		return RawDomainRecord{}, errors.New("postgres pool is nil")
	}
	payload, err := validateRawDomainRecord(&rec)
	if err != nil {
		return RawDomainRecord{}, err
	}

	var (
		createdAt time.Time
		updatedAt time.Time
	)
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "upsert_record_json")
	if err != nil {
		return RawDomainRecord{}, err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	err = conn.QueryRow(queryCtx, upsertRecordJSONSQL, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID, payload, rec.OrganizationID).Scan(&createdAt, &updatedAt)
	recordDatabaseOperation("upsert_record_json", start, err)
	if err != nil {
		return RawDomainRecord{}, err
	}

	rec.DataJSON = payload
	rec.CreatedAt = createdAt.UTC()
	rec.UpdatedAt = updatedAt.UTC()
	return rec, nil
}

func (db *PostgresDB) GetRecord(ctx context.Context, domain, collection, organizationID, recordID string) (DomainRecord, bool, error) {
	if db == nil || db.pool == nil {
		return DomainRecord{}, false, errors.New("postgres pool is nil")
	}
	raw, found, err := db.GetRecordJSON(ctx, domain, collection, organizationID, recordID)
	if err != nil || !found {
		return DomainRecord{}, found, err
	}
	data, err := parseDataJSON(raw.DataJSON)
	if err != nil {
		return DomainRecord{}, false, err
	}
	return DomainRecord{
		Domain:         raw.Domain,
		Collection:     raw.Collection,
		OrganizationID: raw.OrganizationID,
		RecordID:       raw.RecordID,
		Data:           data,
		CreatedAt:      raw.CreatedAt,
		UpdatedAt:      raw.UpdatedAt,
	}, true, nil
}

func (db *PostgresDB) GetRecordJSON(ctx context.Context, domain, collection, organizationID, recordID string) (RawDomainRecord, bool, error) {
	if db == nil || db.pool == nil {
		return RawDomainRecord{}, false, errors.New("postgres pool is nil")
	}

	var (
		dataRaw   []byte
		createdAt time.Time
		updatedAt time.Time
	)
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "get_record_json")
	if err != nil {
		return RawDomainRecord{}, false, err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)
	recordID = strings.TrimSpace(recordID)
	err = conn.QueryRow(queryCtx, `
		SELECT data::text, created_at, updated_at
		FROM governance_state_records
		WHERE domain = $1
		  AND collection_name = $2
		  AND organization_id = $3
		  AND record_id = $4
	`, domain, collection, organizationID, recordID).Scan(&dataRaw, &createdAt, &updatedAt)
	recordDatabaseOperation("get_record_json", start, err)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RawDomainRecord{}, false, nil
		}
		return RawDomainRecord{}, false, err
	}

	return RawDomainRecord{
		Domain:         domain,
		Collection:     collection,
		OrganizationID: organizationID,
		RecordID:       recordID,
		DataJSON:       append([]byte(nil), dataRaw...),
		CreatedAt:      createdAt.UTC(),
		UpdatedAt:      updatedAt.UTC(),
	}, true, nil
}

func (db *PostgresDB) ListRecords(ctx context.Context, domain, collection, organizationID string, filters map[string]any, limit int) ([]DomainRecord, error) {
	return db.ListRecordsWithOptions(ctx, domain, collection, organizationID, filters, StateListOptions{Limit: limit})
}

func (db *PostgresDB) ListRecordsWithOptions(ctx context.Context, domain, collection, organizationID string, filters map[string]any, options StateListOptions) ([]DomainRecord, error) {
	filters = copyMap(filters)
	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)

	where, args, pushedAllFilters := buildPostgresRecordWhere(domain, collection, organizationID, filters, 1)
	if !pushedAllFilters && options.RequirePushdown {
		return nil, ErrUnsupportedFilterShape
	}
	if db == nil || db.pool == nil {
		return nil, errors.New("postgres pool is nil")
	}

	limit := options.Limit
	recheckBudget := normalizedRecheckRowBudget(options.RecheckRowBudget)
	queryLimit := limit
	if !pushedAllFilters {
		queryLimit = recheckBudget
	}

	query := `
		SELECT domain, collection_name, organization_id, record_id, data, created_at, updated_at
		FROM governance_state_records
		WHERE ` + where + `
		ORDER BY updated_at DESC, record_id ASC
	`
	if queryLimit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", len(args)+1)
		args = append(args, queryLimit)
	}

	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "list_records")
	if err != nil {
		return nil, err
	}
	rows, err := conn.Query(queryCtx, query, args...)
	if err != nil {
		conn.Release()
		cancel()
		recordDatabaseOperation("list_records", start, err)
		return nil, err
	}
	var opErr error
	defer func() {
		rows.Close()
		conn.Release()
		cancel()
		recordDatabaseOperation("list_records", start, opErr)
	}()

	recordCap := 64
	if limit > 0 && limit < recordCap {
		recordCap = limit
	}
	records := make([]DomainRecord, 0, recordCap)
	scanned := 0
	for rows.Next() {
		scanned++
		var (
			rec     DomainRecord
			dataRaw []byte
			created time.Time
			updated time.Time
		)
		if err := rows.Scan(&rec.Domain, &rec.Collection, &rec.OrganizationID, &rec.RecordID, &dataRaw, &created, &updated); err != nil {
			opErr = err
			return nil, err
		}
		data, err := parseDataJSON(dataRaw)
		if err != nil {
			opErr = err
			return nil, err
		}
		if !matchesFilter(data, filters) {
			continue
		}
		rec.Data = data
		rec.CreatedAt = created.UTC()
		rec.UpdatedAt = updated.UTC()
		records = append(records, rec)
		if limit > 0 && !pushedAllFilters && len(records) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		opErr = err
		return nil, err
	}
	if !pushedAllFilters && scanned >= recheckBudget && (limit <= 0 || len(records) < limit) {
		opErr = ErrQueryLimitReached
		return nil, fmt.Errorf("%w: scanned %d rows for unsupported filter shape", ErrQueryLimitReached, scanned)
	}
	return records, nil
}

func (db *PostgresDB) CountRecords(ctx context.Context, domain, collection, organizationID string, filters map[string]any) (int64, error) {
	return db.CountRecordsWithOptions(ctx, domain, collection, organizationID, filters, StateCountOptions{})
}

func (db *PostgresDB) CountRecordsWithOptions(ctx context.Context, domain, collection, organizationID string, filters map[string]any, options StateCountOptions) (int64, error) {
	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)

	where, args, pushedAllFilters := buildPostgresRecordWhere(domain, collection, organizationID, copyMap(filters), 1)
	if !pushedAllFilters {
		if options.RequirePushdown {
			return 0, ErrUnsupportedFilterShape
		}
		if db == nil || db.pool == nil {
			return 0, errors.New("postgres pool is nil")
		}
		items, err := db.ListRecordsWithOptions(ctx, domain, collection, organizationID, filters, StateListOptions{
			Limit:            normalizedRecheckRowBudget(options.RecheckRowBudget),
			RecheckRowBudget: options.RecheckRowBudget,
		})
		if err != nil {
			return 0, err
		}
		return int64(len(items)), nil
	}
	if db == nil || db.pool == nil {
		return 0, errors.New("postgres pool is nil")
	}

	var count int64
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "count_records")
	if err != nil {
		return 0, err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	query := `SELECT COUNT(*) FROM governance_state_records WHERE ` + where
	err = conn.QueryRow(queryCtx, query, args...).Scan(&count)
	recordDatabaseOperation("count_records", start, err)
	return count, err
}

func (db *PostgresDB) EstimateCount(ctx context.Context, domain, collection, organizationID string) (int64, error) {
	if db == nil || db.pool == nil {
		return 0, errors.New("postgres pool is nil")
	}

	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)

	// If no filters, use the fastest catalog-based estimate
	if domain == "" && collection == "" && organizationID == "" {
		var estimate int64
		conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "estimate_count")
		if err != nil {
			return 0, err
		}
		defer func() {
			conn.Release()
			cancel()
		}()
		err = conn.QueryRow(queryCtx, `
			SELECT reltuples::bigint 
			FROM pg_class 
			WHERE oid = 'governance_state_records'::regclass
		`).Scan(&estimate)
		recordDatabaseOperation("estimate_count", start, err)
		return estimate, err
	}

	// For scoped queries, use EXPLAIN to get the planner's estimate
	args := make([]any, 0, 3)
	clauses := make([]string, 0, 3)
	argPos := 1
	if domain != "" {
		clauses = append(clauses, fmt.Sprintf("domain = $%d", argPos))
		args = append(args, domain)
		argPos++
	}
	if collection != "" {
		clauses = append(clauses, fmt.Sprintf("collection_name = $%d", argPos))
		args = append(args, collection)
		argPos++
	}
	if organizationID != "" {
		clauses = append(clauses, fmt.Sprintf("organization_id = $%d", argPos))
		args = append(args, organizationID)
		argPos++
	}

	query := `EXPLAIN (FORMAT JSON) SELECT 1 FROM governance_state_records WHERE ` + strings.Join(clauses, " AND ")
	var planJSON []byte
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "estimate_count")
	if err != nil {
		return 0, err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	err = conn.QueryRow(queryCtx, query, args...).Scan(&planJSON)
	recordDatabaseOperation("estimate_count", start, err)
	if err != nil {
		return 0, err
	}
	return parseExplainPlanRows(planJSON)
}

type explainJSONPlan struct {
	Plan struct {
		PlanRows int64 `json:"Plan Rows"`
	} `json:"Plan"`
}

func parseExplainPlanRows(planJSON []byte) (int64, error) {
	if len(bytes.TrimSpace(planJSON)) == 0 {
		return 0, nil
	}
	var plans []explainJSONPlan
	if err := json.Unmarshal(planJSON, &plans); err != nil {
		return 0, err
	}
	if len(plans) == 0 {
		return 0, nil
	}
	return plans[0].Plan.PlanRows, nil
}

func (db *PostgresDB) DeleteRecord(ctx context.Context, domain, collection, organizationID, recordID string) error {
	if db == nil || db.pool == nil {
		return errors.New("postgres pool is nil")
	}
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "delete_record")
	if err != nil {
		return err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	_, err = conn.Exec(queryCtx, `
		DELETE FROM governance_state_records
		WHERE domain = $1
		  AND collection_name = $2
		  AND organization_id = $3
		  AND record_id = $4
	`, strings.TrimSpace(domain), strings.TrimSpace(collection), strings.TrimSpace(organizationID), strings.TrimSpace(recordID))
	recordDatabaseOperation("delete_record", start, err)
	return err
}

// DeleteRecordsByOrganization removes all governance state records for an organization.
func (db *PostgresDB) DeleteRecordsByOrganization(ctx context.Context, organizationID string) (int64, error) {
	if db == nil || db.pool == nil {
		return 0, errors.New("postgres pool is nil")
	}
	conn, queryCtx, cancel, start, err := db.acquireConn(ctx, "delete_records_by_organization")
	if err != nil {
		return 0, err
	}
	defer func() {
		conn.Release()
		cancel()
	}()
	commandTag, err := conn.Exec(queryCtx, `
		DELETE FROM governance_state_records
		WHERE organization_id = $1
	`, strings.TrimSpace(organizationID))
	recordDatabaseOperation("delete_records_by_organization", start, err)
	if err != nil {
		return 0, err
	}
	return commandTag.RowsAffected(), nil
}

func buildPostgresRecordWhere(domain, collection, organizationID string, filters map[string]any, startArg int) (string, []any, bool) {
	args := make([]any, 0, 3+len(filters)*2)
	clauses := make([]string, 0, 3+len(filters))
	argPos := startArg
	addClause := func(clause string, values ...any) {
		clauses = append(clauses, clause)
		args = append(args, values...)
		argPos += len(values)
	}
	if domain != "" {
		addClause(fmt.Sprintf("domain = $%d", argPos), domain)
	}
	if collection != "" {
		addClause(fmt.Sprintf("collection_name = $%d", argPos), collection)
	}
	if organizationID != "" {
		addClause(fmt.Sprintf("organization_id = $%d", argPos), organizationID)
	}

	pushedAllFilters := true
	filterKeys := make([]string, 0, len(filters))
	for field := range filters {
		filterKeys = append(filterKeys, field)
	}
	sort.Strings(filterKeys)
	for _, field := range filterKeys {
		value, ok := postgresScalarFilterText(filters[field])
		if !ok {
			pushedAllFilters = false
			continue
		}
		expr := "btrim(data ->> " + quoteSQLString(field) + ")"
		addClause(fmt.Sprintf("%s = $%d", expr, argPos), value)
	}

	if len(clauses) == 0 {
		return "TRUE", args, pushedAllFilters
	}
	return strings.Join(clauses, " AND "), args, pushedAllFilters
}

func normalizedRecheckRowBudget(value int) int {
	if value <= 0 {
		return defaultPostgresRecheckRowBudget
	}
	if value > maxPostgresRecheckRowBudget {
		return maxPostgresRecheckRowBudget
	}
	return value
}

func postgresScalarFilterText(value any) (string, bool) {
	if text, ok := comparableString(value); ok {
		return strings.TrimSpace(text), true
	}
	switch typed := value.(type) {
	case float32:
		return strings.TrimSpace(fmt.Sprintf("%v", typed)), true
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%v", typed)), true
	default:
		return "", false
	}
}

func quoteSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func validateRawDomainRecord(rec *RawDomainRecord) ([]byte, error) {
	if rec == nil {
		return nil, errors.New("record is required")
	}
	rec.Domain = strings.TrimSpace(rec.Domain)
	rec.Collection = strings.TrimSpace(rec.Collection)
	rec.OrganizationID = strings.TrimSpace(rec.OrganizationID)
	rec.RecordID = strings.TrimSpace(rec.RecordID)
	if rec.Domain == "" {
		return nil, errors.New("domain is required")
	}
	if rec.Collection == "" {
		return nil, errors.New("collection is required")
	}
	if rec.RecordID == "" {
		return nil, errors.New("record id is required")
	}
	return normalizeDataJSON(rec.DataJSON)
}

func normalizeDataJSON(data []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		trimmed = []byte("{}")
	}
	if !json.Valid(trimmed) {
		return nil, errors.New("data json is invalid")
	}
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return nil, errors.New("data json must be an object")
	}
	return append([]byte(nil), trimmed...), nil
}

func validateDomainRecord(rec *DomainRecord) error {
	if rec == nil {
		return errors.New("record is required")
	}
	rec.Domain = strings.TrimSpace(rec.Domain)
	rec.Collection = strings.TrimSpace(rec.Collection)
	rec.OrganizationID = strings.TrimSpace(rec.OrganizationID)
	rec.RecordID = strings.TrimSpace(rec.RecordID)
	rec.Data = copyMap(rec.Data)
	if rec.Data == nil {
		rec.Data = map[string]any{}
	}
	if rec.OrganizationID != "" {
		rec.Data["organization_id"] = rec.OrganizationID
	}
	if rec.Domain == "" {
		return errors.New("domain is required")
	}
	if rec.Collection == "" {
		return errors.New("collection is required")
	}
	if rec.RecordID == "" {
		return errors.New("record id is required")
	}
	return nil
}

func parseDataJSON(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	parsed := map[string]any{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	if parsed == nil {
		return map[string]any{}, nil
	}
	return parsed, nil
}
