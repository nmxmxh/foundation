package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	conn, queryCtx, lease, start, err := db.acquireConn(ctx, "copy_from")
	if err != nil {
		return 0, err
	}
	defer func() {
		conn.Release()
		lease.release()
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
	conn, queryCtx, lease, start, err := db.acquireConn(ctx, "send_batch")
	if err != nil {
		return err
	}
	defer func() {
		conn.Release()
		lease.release()
	}()
	results := conn.Conn().SendBatch(queryCtx, &batch)
	defer results.Close()
	err = consume(results)
	err = normalizePostgresOperationError(contextErr(queryCtx), err)
	recordDatabaseOperation("send_batch", start, err)
	return err
}

// upsertRecordsUnnestSQL is the set-based batch form of upsertRecordSQL: one
// statement, one parse/plan, all rows via unnest arrays. The upsert CTE keeps
// the exact single-row semantics (ON CONFLICT identity, IS DISTINCT FROM
// change detection, updated_at bump only on change); the outer join maps each
// input ordinal to its timestamps — inserted/changed rows come from the CTE's
// RETURNING, unchanged rows read their existing row (unmodified by this
// statement, so the pre-statement snapshot is the correct value).
const upsertRecordsUnnestSQL = `
	WITH input AS (
		SELECT t.domain, t.collection_name, t.organization_id, t.record_id, t.data, t.ord
		FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::jsonb[])
			WITH ORDINALITY AS t(domain, collection_name, organization_id, record_id, data, ord)
	), upsert AS (
		INSERT INTO governance_state_records (domain, collection_name, organization_id, record_id, data)
		SELECT domain, collection_name, organization_id, record_id, data FROM input
		ON CONFLICT (domain, collection_name, organization_id, record_id)
		DO UPDATE SET data = EXCLUDED.data, updated_at = NOW()
		WHERE governance_state_records.data IS DISTINCT FROM EXCLUDED.data
		RETURNING domain, collection_name, organization_id, record_id, created_at, updated_at
	)
	SELECT COALESCE(u.created_at, g.created_at), COALESCE(u.updated_at, g.updated_at)
	FROM input i
	LEFT JOIN upsert u ON u.domain = i.domain AND u.collection_name = i.collection_name
		AND u.organization_id = i.organization_id AND u.record_id = i.record_id
	LEFT JOIN governance_state_records g ON g.domain = i.domain AND g.collection_name = i.collection_name
		AND g.organization_id = i.organization_id AND g.record_id = i.record_id
	ORDER BY i.ord`

// batchUpsertInput is the validated, deduplicated array form of a record batch
// ready for upsertRecordsUnnestSQL.
type batchUpsertInput struct {
	out           []DomainRecord
	domains       []string
	collections   []string
	organizations []string
	recordIDs     []string
	payloads      []string
	// rowFor maps each input index to its identity's array slot so duplicate
	// positions receive the final row's timestamps.
	rowFor []int
}

// buildBatchUpsertInput validates every record, marshals payloads, and
// deduplicates identities keep-last — ON CONFLICT DO UPDATE cannot touch one
// row twice per statement, and keep-last is exactly the sequential
// UpsertRecord outcome.
func buildBatchUpsertInput(records []DomainRecord) (batchUpsertInput, error) {
	input := batchUpsertInput{
		out:           make([]DomainRecord, len(records)),
		domains:       make([]string, 0, len(records)),
		collections:   make([]string, 0, len(records)),
		organizations: make([]string, 0, len(records)),
		recordIDs:     make([]string, 0, len(records)),
		payloads:      make([]string, 0, len(records)),
		rowFor:        make([]int, len(records)),
	}
	type identity struct{ domain, collection, organization, record string }
	slot := make(map[identity]int, len(records))
	for i := range records {
		rec := records[i]
		if err := validateDomainRecord(&rec); err != nil {
			return batchUpsertInput{}, err
		}
		payload, err := json.Marshal(rec.Data)
		if err != nil {
			return batchUpsertInput{}, err
		}
		input.out[i] = rec
		id := identity{rec.Domain, rec.Collection, rec.OrganizationID, rec.RecordID}
		if at, seen := slot[id]; seen {
			input.payloads[at] = string(payload)
			input.rowFor[i] = at
			continue
		}
		slot[id] = len(input.domains)
		input.rowFor[i] = len(input.domains)
		input.domains = append(input.domains, rec.Domain)
		input.collections = append(input.collections, rec.Collection)
		input.organizations = append(input.organizations, rec.OrganizationID)
		input.recordIDs = append(input.recordIDs, rec.RecordID)
		input.payloads = append(input.payloads, string(payload))
	}
	return input, nil
}

// UpsertRecordsBatch upserts many domain records in ONE SQL statement (unnest
// arrays), so high-rate projection mirrors pay one round trip and one
// parse/plan per batch instead of per record.
//
// Refinement contract (vs sequential UpsertRecord per record, proven by the
// service-backed parity test): same final rows, same change detection
// (unchanged data never bumps updated_at), and last-write-wins for duplicate
// identities within one batch. Duplicates are deduplicated client-side keeping
// the last occurrence — ON CONFLICT DO UPDATE cannot touch the same row twice
// in one statement — which is exactly the sequential outcome; every input
// position still receives the final row's timestamps. The statement is
// individually atomic and idempotent, so a failed batch replays safely.
func (db *PostgresDB) UpsertRecordsBatch(ctx context.Context, records []DomainRecord) ([]DomainRecord, error) {
	if db == nil || db.pool == nil {
		return nil, errors.New("postgres pool is nil")
	}
	if len(records) == 0 {
		return nil, nil
	}

	input, err := buildBatchUpsertInput(records)
	if err != nil {
		return nil, err
	}
	out := input.out
	rowFor := input.rowFor
	domains := input.domains

	conn, queryCtx, lease, start, err := db.acquireConn(ctx, "upsert_records_batch")
	if err != nil {
		return nil, err
	}
	defer func() {
		conn.Release()
		lease.release()
	}()

	rows, err := conn.Query(queryCtx, upsertRecordsUnnestSQL, input.domains, input.collections, input.organizations, input.recordIDs, input.payloads)
	if err != nil {
		err = normalizePostgresOperationError(contextErr(queryCtx), err)
		recordDatabaseOperation("upsert_records_batch", start, err)
		return nil, err
	}
	createdAt := make([]time.Time, 0, len(domains))
	updatedAt := make([]time.Time, 0, len(domains))
	for rows.Next() {
		var created, updated time.Time
		if err := rows.Scan(&created, &updated); err != nil {
			rows.Close()
			recordDatabaseOperation("upsert_records_batch", start, err)
			return nil, err
		}
		createdAt = append(createdAt, created.UTC())
		updatedAt = append(updatedAt, updated.UTC())
	}
	rows.Close()
	err = normalizePostgresOperationError(contextErr(queryCtx), rows.Err())
	recordDatabaseOperation("upsert_records_batch", start, err)
	if err != nil {
		return nil, err
	}
	if len(createdAt) != len(domains) {
		return nil, fmt.Errorf("upsert_records_batch returned %d rows, want %d", len(createdAt), len(domains))
	}
	for i := range out {
		out[i].CreatedAt = createdAt[rowFor[i]]
		out[i].UpdatedAt = updatedAt[rowFor[i]]
	}
	return out, nil
}
