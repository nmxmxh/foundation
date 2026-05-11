package database

import (
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
)

const maxInt32Value = 2_147_483_647

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
	row    RowScanner
	cancel context.CancelFunc
}

type budgetedRows struct {
	rows   pgx.Rows
	cancel context.CancelFunc
}

func (r budgetedRow) Scan(dest ...any) error {
	defer r.cancel()
	return r.row.Scan(dest...)
}

func (r budgetedRows) Close() {
	r.rows.Close()
	r.cancel()
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
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	return &postgresTx{tx: tx}, nil
}

func (db *PostgresDB) Exec(ctx context.Context, query string, args ...any) error {
	if db == nil || db.pool == nil {
		return errors.New("postgres pool is nil")
	}
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	defer cancel()
	_, err := db.pool.Exec(ctx, query, args...)
	return err
}

func (db *PostgresDB) ExecResult(ctx context.Context, query string, args ...any) (CommandResult, error) {
	if db == nil || db.pool == nil {
		return nil, errors.New("postgres pool is nil")
	}
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	defer cancel()
	tag, err := db.pool.Exec(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return postgresCommandResult{tag: tag}, nil
}

func (db *PostgresDB) QueryRow(ctx context.Context, query string, args ...any) RowScanner {
	if db == nil || db.pool == nil {
		return memoryRow{err: errors.New("postgres pool is nil")}
	}
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	return budgetedRow{row: db.pool.QueryRow(ctx, query, args...), cancel: cancel}
}

func (db *PostgresDB) Query(ctx context.Context, query string, args ...any) (Rows, error) {
	if db == nil || db.pool == nil {
		return nil, errors.New("postgres pool is nil")
	}
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		cancel()
		return nil, err
	}
	return budgetedRows{rows: rows, cancel: cancel}, nil
}

func (db *PostgresDB) QueryMaps(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if db == nil || db.pool == nil {
		return nil, errors.New("postgres pool is nil")
	}
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	defer cancel()
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanToMaps(rows)
}

func (db *PostgresDB) Stats() StoreStats {
	if db == nil || db.pool == nil {
		return StoreStats{}
	}
	s := db.pool.Stat()
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
		for i := 0; i < numFields; i++ {
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
		dataRaw   []byte
		createdAt time.Time
		updatedAt time.Time
	)
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	defer cancel()
	err = db.pool.QueryRow(ctx, `
		INSERT INTO governance_state_records (domain, collection_name, organization_id, record_id, data)
		VALUES ($1, $2, $3, $4, $5::jsonb)
		ON CONFLICT (domain, collection_name, organization_id, record_id)
		DO UPDATE SET data = EXCLUDED.data, updated_at = NOW()
		RETURNING data, created_at, updated_at
	`, rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID, payload).Scan(&dataRaw, &createdAt, &updatedAt)
	if err != nil {
		return DomainRecord{}, err
	}

	parsed, err := parseDataJSON(dataRaw)
	if err != nil {
		return DomainRecord{}, err
	}
	rec.Data = parsed
	rec.CreatedAt = createdAt.UTC()
	rec.UpdatedAt = updatedAt.UTC()
	return rec, nil
}

func (db *PostgresDB) GetRecord(ctx context.Context, domain, collection, organizationID, recordID string) (DomainRecord, bool, error) {
	if db == nil || db.pool == nil {
		return DomainRecord{}, false, errors.New("postgres pool is nil")
	}

	var (
		dataRaw   []byte
		createdAt time.Time
		updatedAt time.Time
	)
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	defer cancel()
	err := db.pool.QueryRow(ctx, `
		SELECT data, created_at, updated_at
		FROM governance_state_records
		WHERE domain = $1
		  AND collection_name = $2
		  AND organization_id = $3
		  AND record_id = $4
	`, strings.TrimSpace(domain), strings.TrimSpace(collection), strings.TrimSpace(organizationID), strings.TrimSpace(recordID)).Scan(&dataRaw, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DomainRecord{}, false, nil
		}
		return DomainRecord{}, false, err
	}

	data, err := parseDataJSON(dataRaw)
	if err != nil {
		return DomainRecord{}, false, err
	}
	return DomainRecord{
		Domain:         strings.TrimSpace(domain),
		Collection:     strings.TrimSpace(collection),
		OrganizationID: strings.TrimSpace(organizationID),
		RecordID:       strings.TrimSpace(recordID),
		Data:           data,
		CreatedAt:      createdAt.UTC(),
		UpdatedAt:      updatedAt.UTC(),
	}, true, nil
}

func (db *PostgresDB) ListRecords(ctx context.Context, domain, collection, organizationID string, filters map[string]any, limit int) ([]DomainRecord, error) {
	if db == nil || db.pool == nil {
		return nil, errors.New("postgres pool is nil")
	}

	filters = copyMap(filters)
	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)

	where, args, pushedAllFilters := buildPostgresRecordWhere(domain, collection, organizationID, filters, 1)

	query := `
		SELECT domain, collection_name, organization_id, record_id, data, created_at, updated_at
		FROM governance_state_records
		WHERE ` + where + `
		ORDER BY updated_at DESC, record_id ASC
	`
	if limit > 0 && pushedAllFilters {
		query += fmt.Sprintf(" LIMIT $%d", len(args)+1)
		args = append(args, limit)
	}

	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	defer cancel()
	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]DomainRecord, 0, 64)
	for rows.Next() {
		var (
			rec     DomainRecord
			dataRaw []byte
			created time.Time
			updated time.Time
		)
		if err := rows.Scan(&rec.Domain, &rec.Collection, &rec.OrganizationID, &rec.RecordID, &dataRaw, &created, &updated); err != nil {
			return nil, err
		}
		data, err := parseDataJSON(dataRaw)
		if err != nil {
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
		return nil, err
	}
	return records, nil
}

func (db *PostgresDB) CountRecords(ctx context.Context, domain, collection, organizationID string, filters map[string]any) (int64, error) {
	if db == nil || db.pool == nil {
		return 0, errors.New("postgres pool is nil")
	}

	domain = strings.TrimSpace(domain)
	collection = strings.TrimSpace(collection)
	organizationID = strings.TrimSpace(organizationID)

	where, args, pushedAllFilters := buildPostgresRecordWhere(domain, collection, organizationID, copyMap(filters), 1)
	if !pushedAllFilters {
		items, err := db.ListRecords(ctx, domain, collection, organizationID, filters, 0)
		if err != nil {
			return 0, err
		}
		return int64(len(items)), nil
	}

	var count int64
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	defer cancel()
	query := `SELECT COUNT(*) FROM governance_state_records WHERE ` + where
	err := db.pool.QueryRow(ctx, query, args...).Scan(&count)
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
		var cancel context.CancelFunc
		ctx, cancel = QueryBudgetContext(ctx, db.opts)
		defer cancel()
		err := db.pool.QueryRow(ctx, `
			SELECT reltuples::bigint 
			FROM pg_class 
			WHERE oid = 'governance_state_records'::regclass
		`).Scan(&estimate)
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

	query := `EXPLAIN SELECT 1 FROM governance_state_records WHERE ` + strings.Join(clauses, " AND ")
	var plan string
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	defer cancel()
	err := db.pool.QueryRow(ctx, query, args...).Scan(&plan)
	if err != nil {
		return 0, err
	}

	// Parse "rows=X" from the EXPLAIN output
	// Example: Seq Scan on governance_state_records  (cost=0.00..12.75 rows=110 width=4)
	start := strings.Index(plan, "rows=")
	if start == -1 {
		return 0, nil
	}
	start += 5
	end := strings.Index(plan[start:], " ")
	if end == -1 {
		end = len(plan[start:])
	}

	var count int64
	fmt.Sscanf(plan[start:start+end], "%d", &count)
	return count, nil
}

func (db *PostgresDB) DeleteRecord(ctx context.Context, domain, collection, organizationID, recordID string) error {
	if db == nil || db.pool == nil {
		return errors.New("postgres pool is nil")
	}
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	defer cancel()
	_, err := db.pool.Exec(ctx, `
		DELETE FROM governance_state_records
		WHERE domain = $1
		  AND collection_name = $2
		  AND organization_id = $3
		  AND record_id = $4
	`, strings.TrimSpace(domain), strings.TrimSpace(collection), strings.TrimSpace(organizationID), strings.TrimSpace(recordID))
	return err
}

// DeleteRecordsByOrganization removes all governance state records for an organization.
func (db *PostgresDB) DeleteRecordsByOrganization(ctx context.Context, organizationID string) (int64, error) {
	if db == nil || db.pool == nil {
		return 0, errors.New("postgres pool is nil")
	}
	var cancel context.CancelFunc
	ctx, cancel = QueryBudgetContext(ctx, db.opts)
	defer cancel()
	commandTag, err := db.pool.Exec(ctx, `
		DELETE FROM governance_state_records
		WHERE organization_id = $1
	`, strings.TrimSpace(organizationID))
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
