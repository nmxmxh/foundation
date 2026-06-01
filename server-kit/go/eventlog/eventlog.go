// Package eventlog owns the durable fact lane between Postgres and short-lived
// delivery substrates such as Redis Streams.
package eventlog

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	metautil "github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

const (
	DefaultTable       = "foundation_event_log"
	DefaultStreamField = "envelope"
	DefaultMaxAttempts = 25
	DefaultBatchLimit  = 128
	MaxBatchLimit      = 1024
	DefaultClaimTTL    = 30 * time.Second
)

var (
	ErrStoreRequired    = errors.New("eventlog store is required")
	ErrStreamRequired   = errors.New("eventlog stream is required")
	ErrAppenderRequired = errors.New("eventlog stream appender is required")
	ErrPublishClaimLost = errors.New("eventlog publish claim was lost")
)

type StreamAppender interface {
	XAdd(ctx context.Context, stream string, values map[string]any) (string, error)
}

type streamBatchAppender interface {
	XAddMany(ctx context.Context, stream string, entries []map[string]any) ([]string, []error)
}

type Entry struct {
	ID               int64
	EventID          string
	EventType        string
	OrganizationID   string
	CorrelationID    string
	SchemaVersion    string
	PayloadEncoding  string
	Envelope         []byte
	Metadata         map[string]any
	OccurredAt       time.Time
	CreatedAt        time.Time
	PublishedAt      *time.Time
	PublishAttempts  int
	LastPublishError string
}

type PublishOptions struct {
	Stream      string
	Field       string
	Limit       int
	MaxAttempts int
	ClaimTTL    time.Duration
	ClaimToken  string
}

type PublishResult struct {
	Read      int
	Published int
	Failed    int
}

// Append persists a typed event envelope. Callers may pass a transaction that
// also owns the domain state mutation; the envelope is stored as binary protobuf
// so downstream Redis/Hermes paths avoid JSON materialization.
func Append(ctx context.Context, db database.DBTX, envelope events.Envelope) (Entry, error) {
	if db == nil {
		return Entry{}, ErrStoreRequired
	}
	ctx = normalizeContext(ctx)
	if err := ctxErr(ctx); err != nil {
		return Entry{}, err
	}
	envelope.Normalize()
	if strings.TrimSpace(envelope.ID) == "" {
		id, err := newEventID()
		if err != nil {
			return Entry{}, err
		}
		envelope.ID = id
	}
	if err := envelope.Validate(); err != nil {
		return Entry{}, err
	}
	raw, err := envelope.ToBinary()
	if err != nil {
		return Entry{}, err
	}
	metadataJSON, err := json.Marshal(envelope.Metadata)
	if err != nil {
		return Entry{}, err
	}

	entry := Entry{
		EventID:         envelope.ID,
		EventType:       envelope.EventType,
		OrganizationID:  organizationID(envelope.Metadata),
		CorrelationID:   envelope.CorrelationID,
		SchemaVersion:   envelope.SchemaVersion,
		PayloadEncoding: envelope.PayloadEncoding,
		Envelope:        append([]byte(nil), raw...),
		Metadata:        copyMap(envelope.Metadata),
		OccurredAt:      envelope.Timestamp.UTC(),
	}
	const query = `
		INSERT INTO foundation_event_log (
			event_id,
			event_type,
			organization_id,
			correlation_id,
			schema_version,
			payload_encoding,
			envelope,
			metadata,
			occurred_at,
			source_node_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10)
		RETURNING id, event_id, created_at
	`
	err = db.QueryRow(ctx, query,
		entry.EventID,
		entry.EventType,
		entry.OrganizationID,
		entry.CorrelationID,
		entry.SchemaVersion,
		entry.PayloadEncoding,
		entry.Envelope,
		metadataJSON,
		entry.OccurredAt,
		envelope.SourceNodeID,
	).Scan(&entry.ID, &entry.EventID, &entry.CreatedAt)
	if err != nil {
		return Entry{}, err
	}
	entry.CreatedAt = entry.CreatedAt.UTC()
	return entry, nil
}

func FetchPending(ctx context.Context, db database.DBTX, limit int, maxAttempts int) ([]Entry, error) {
	if db == nil {
		return nil, ErrStoreRequired
	}
	ctx = normalizeContext(ctx)
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	limit = normalizeLimit(limit)
	maxAttempts = normalizeMaxAttempts(maxAttempts)
	if queryer, ok := db.(database.RowQueryer); ok {
		return fetchPendingRows(ctx, queryer, limit, maxAttempts)
	}
	const query = `
		SELECT
			id,
			event_id,
			event_type,
			organization_id,
			correlation_id,
			schema_version,
			payload_encoding,
			encode(envelope, 'base64') AS envelope_base64,
			metadata::text AS metadata_json,
			occurred_at,
			created_at,
			publish_attempts,
			COALESCE(last_publish_error, '') AS last_publish_error
		FROM foundation_event_log
		WHERE published_at IS NULL
		  AND publish_attempts < $1
		ORDER BY id ASC
		LIMIT $2
	`
	rows, err := db.QueryMaps(ctx, query, maxAttempts, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		entry, err := entryFromMap(row)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func fetchPendingRows(ctx context.Context, db database.RowQueryer, limit int, maxAttempts int) ([]Entry, error) {
	const query = `
		SELECT
			id,
			event_id,
			event_type,
			organization_id,
			correlation_id,
			schema_version,
			payload_encoding,
			envelope,
			metadata::text AS metadata_json,
			occurred_at,
			created_at,
			publish_attempts,
			COALESCE(last_publish_error, '') AS last_publish_error
		FROM foundation_event_log
		WHERE published_at IS NULL
		  AND publish_attempts < $1
		ORDER BY id ASC
		LIMIT $2
	`
	rows, err := db.Query(ctx, query, maxAttempts, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]Entry, 0, limit)
	for rows.Next() {
		entry, err := scanPendingEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func claimPendingRows(
	ctx context.Context,
	db database.RowQueryer,
	limit int,
	maxAttempts int,
	claimToken string,
	claimTTL time.Duration,
) ([]Entry, error) {
	const query = `
		WITH candidates AS (
			SELECT id
			FROM foundation_event_log
			WHERE published_at IS NULL
			  AND publish_attempts < $1
			  AND (
				publish_claim_expires_at IS NULL
				OR publish_claim_expires_at <= NOW()
			  )
			ORDER BY id ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE foundation_event_log AS event_log
		SET publish_claim_token = $3,
			publish_claimed_at = NOW(),
			publish_claim_expires_at = NOW() + ($4::bigint * INTERVAL '1 millisecond'),
			updated_at = NOW()
		FROM candidates
		WHERE event_log.id = candidates.id
		RETURNING
			event_log.id,
			event_log.event_id,
			event_log.event_type,
			event_log.organization_id,
			event_log.correlation_id,
			event_log.schema_version,
			event_log.payload_encoding,
			event_log.envelope,
			event_log.metadata::text AS metadata_json,
			event_log.occurred_at,
			event_log.created_at,
			event_log.publish_attempts,
			COALESCE(event_log.last_publish_error, '') AS last_publish_error
	`
	rows, err := db.Query(ctx, query, maxAttempts, limit, claimToken, claimTTL.Milliseconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]Entry, 0, limit)
	for rows.Next() {
		entry, err := scanPendingEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func scanPendingEntry(row database.RowScanner) (Entry, error) {
	var entry Entry
	var envelope []byte
	var metadataJSON string
	var publishAttempts int64
	if err := row.Scan(
		&entry.ID,
		&entry.EventID,
		&entry.EventType,
		&entry.OrganizationID,
		&entry.CorrelationID,
		&entry.SchemaVersion,
		&entry.PayloadEncoding,
		&envelope,
		&metadataJSON,
		&entry.OccurredAt,
		&entry.CreatedAt,
		&publishAttempts,
		&entry.LastPublishError,
	); err != nil {
		return Entry{}, err
	}
	entry.Envelope = append([]byte(nil), envelope...)
	entry.PublishAttempts = int(publishAttempts)
	entry.OccurredAt = entry.OccurredAt.UTC()
	entry.CreatedAt = entry.CreatedAt.UTC()
	entry.Metadata = map[string]any{}
	if rawMetadata := strings.TrimSpace(metadataJSON); rawMetadata != "" {
		if err := json.Unmarshal([]byte(rawMetadata), &entry.Metadata); err != nil {
			return Entry{}, err
		}
	}
	return entry, nil
}

func PublishPending(ctx context.Context, db database.DBTX, appender StreamAppender, opts PublishOptions) (PublishResult, error) {
	if appender == nil {
		return PublishResult{}, ErrAppenderRequired
	}
	if db == nil {
		return PublishResult{}, ErrStoreRequired
	}
	ctx = normalizeContext(ctx)
	stream := strings.TrimSpace(opts.Stream)
	if stream == "" {
		return PublishResult{}, ErrStreamRequired
	}
	field := strings.TrimSpace(opts.Field)
	if field == "" {
		field = DefaultStreamField
	}
	entries, claimToken, err := pendingEntriesForPublish(ctx, db, opts)
	if err != nil {
		return PublishResult{}, err
	}
	result := PublishResult{Read: len(entries)}
	if len(entries) > 1 {
		if batchAppender, ok := appender.(streamBatchAppender); ok {
			return publishPendingBatch(ctx, db, batchAppender, stream, field, entries, claimToken, result)
		}
	}
	for _, entry := range entries {
		if err := ctxErr(ctx); err != nil {
			return result, err
		}
		streamID, err := appender.XAdd(ctx, stream, map[string]any{field: entry.Envelope})
		if err != nil {
			result.Failed++
			_ = markPublishFailed(ctx, db, entry.ID, err, claimToken)
			return result, err
		}
		if err := markPublished(ctx, db, entry.ID, stream, streamID, claimToken); err != nil {
			return result, err
		}
		result.Published++
	}
	return result, nil
}

func pendingEntriesForPublish(ctx context.Context, db database.DBTX, opts PublishOptions) ([]Entry, string, error) {
	if queryer, ok := db.(database.RowQueryer); ok {
		claimToken := strings.TrimSpace(opts.ClaimToken)
		if claimToken == "" {
			generated, err := newClaimToken()
			if err != nil {
				return nil, "", err
			}
			claimToken = generated
		}
		entries, err := claimPendingRows(
			ctx,
			queryer,
			normalizeLimit(opts.Limit),
			normalizeMaxAttempts(opts.MaxAttempts),
			claimToken,
			normalizeClaimTTL(opts.ClaimTTL),
		)
		return entries, claimToken, err
	}
	entries, err := FetchPending(ctx, db, opts.Limit, opts.MaxAttempts)
	return entries, "", err
}

func publishPendingBatch(
	ctx context.Context,
	db database.DBTX,
	appender streamBatchAppender,
	stream string,
	field string,
	entries []Entry,
	claimToken string,
	result PublishResult,
) (PublishResult, error) {
	values := make([]map[string]any, len(entries))
	for i, entry := range entries {
		values[i] = map[string]any{field: entry.Envelope}
	}
	streamIDs, appendErrs := appender.XAddMany(ctx, stream, values)
	if len(streamIDs) != len(entries) || len(appendErrs) != len(entries) {
		err := fmt.Errorf("stream batch append returned %d ids and %d errors for %d entries", len(streamIDs), len(appendErrs), len(entries))
		return result, err
	}
	published := make([]PublishedEntry, 0, len(entries))
	var firstErr error
	for i, entry := range entries {
		if err := ctxErr(ctx); err != nil {
			return result, err
		}
		if err := appendErrs[i]; err != nil {
			result.Failed++
			if markErr := markPublishFailed(ctx, db, entry.ID, err, claimToken); markErr != nil && firstErr == nil {
				firstErr = errors.Join(err, markErr)
			} else if firstErr == nil {
				firstErr = err
			}
			continue
		}
		published = append(published, PublishedEntry{ID: entry.ID, Stream: stream, StreamID: streamIDs[i]})
		result.Published++
	}
	if err := markPublishedBatch(ctx, db, published, claimToken); err != nil {
		return result, err
	}
	return result, firstErr
}

func MarkPublished(ctx context.Context, db database.DBTX, id int64, stream string, streamID string) error {
	return markPublished(ctx, db, id, stream, streamID, "")
}

func markPublished(ctx context.Context, db database.DBTX, id int64, stream string, streamID string, claimToken string) error {
	if db == nil {
		return ErrStoreRequired
	}
	ctx = normalizeContext(ctx)
	claimToken = strings.TrimSpace(claimToken)
	if claimToken != "" {
		const query = `
			UPDATE foundation_event_log
			SET published_at = NOW(),
				publish_stream = $2,
				publish_stream_id = $3,
				last_publish_error = NULL,
				publish_claim_token = NULL,
				publish_claimed_at = NULL,
				publish_claim_expires_at = NULL,
				updated_at = NOW()
			WHERE id = $1
			  AND published_at IS NULL
			  AND publish_claim_token = $4
		`
		return execClaimedUpdate(ctx, db, query, 1, id, stream, streamID, claimToken)
	}
	const query = `
		UPDATE foundation_event_log
		SET published_at = NOW(),
			publish_stream = $2,
			publish_stream_id = $3,
			last_publish_error = NULL,
			updated_at = NOW()
		WHERE id = $1
	`
	return db.Exec(ctx, query, id, stream, streamID)
}

type PublishedEntry struct {
	ID       int64
	Stream   string
	StreamID string
}

func MarkPublishedBatch(ctx context.Context, db database.DBTX, entries []PublishedEntry) error {
	return markPublishedBatch(ctx, db, entries, "")
}

func markPublishedBatch(ctx context.Context, db database.DBTX, entries []PublishedEntry, claimToken string) error {
	if db == nil {
		return ErrStoreRequired
	}
	ctx = normalizeContext(ctx)
	if len(entries) == 0 {
		return nil
	}
	if len(entries) == 1 {
		entry := entries[0]
		return markPublished(ctx, db, entry.ID, entry.Stream, entry.StreamID, claimToken)
	}
	ids := make([]int64, len(entries))
	streams := make([]string, len(entries))
	streamIDs := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
		streams[i] = entry.Stream
		streamIDs[i] = entry.StreamID
	}
	claimToken = strings.TrimSpace(claimToken)
	if claimToken != "" {
		const query = `
			UPDATE foundation_event_log AS event_log
			SET published_at = NOW(),
				publish_stream = published.stream,
				publish_stream_id = published.stream_id,
				last_publish_error = NULL,
				publish_claim_token = NULL,
				publish_claimed_at = NULL,
				publish_claim_expires_at = NULL,
				updated_at = NOW()
			FROM (
				SELECT v.id, v.stream, v.stream_id
				FROM UNNEST($1::bigint[], $2::text[], $3::text[]) AS v(id, stream, stream_id)
			) AS published
			WHERE event_log.id = published.id
			  AND event_log.published_at IS NULL
			  AND event_log.publish_claim_token = $4
		`
		return execClaimedUpdate(ctx, db, query, int64(len(entries)), ids, streams, streamIDs, claimToken)
	}
	const query = `
		UPDATE foundation_event_log AS event_log
		SET published_at = NOW(),
			publish_stream = published.stream,
			publish_stream_id = published.stream_id,
			last_publish_error = NULL,
			updated_at = NOW()
		FROM (
			SELECT v.id, v.stream, v.stream_id
			FROM UNNEST($1::bigint[], $2::text[], $3::text[]) AS v(id, stream, stream_id)
		) AS published
		WHERE event_log.id = published.id
	`
	return db.Exec(ctx, query, ids, streams, streamIDs)
}

func MarkPublishFailed(ctx context.Context, db database.DBTX, id int64, cause error) error {
	return markPublishFailed(ctx, db, id, cause, "")
}

func markPublishFailed(ctx context.Context, db database.DBTX, id int64, cause error, claimToken string) error {
	if db == nil {
		return ErrStoreRequired
	}
	ctx = normalizeContext(ctx)
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	if len(message) > 2048 {
		message = message[:2048]
	}
	claimToken = strings.TrimSpace(claimToken)
	if claimToken != "" {
		const query = `
			UPDATE foundation_event_log
			SET publish_attempts = publish_attempts + 1,
				last_publish_error = $2,
				publish_claim_token = NULL,
				publish_claimed_at = NULL,
				publish_claim_expires_at = NULL,
				updated_at = NOW()
			WHERE id = $1
			  AND published_at IS NULL
			  AND publish_claim_token = $3
		`
		return execClaimedUpdate(ctx, db, query, 1, id, message, claimToken)
	}
	const query = `
		UPDATE foundation_event_log
		SET publish_attempts = publish_attempts + 1,
			last_publish_error = $2,
			updated_at = NOW()
		WHERE id = $1
	`
	return db.Exec(ctx, query, id, message)
}

func execClaimedUpdate(ctx context.Context, db database.DBTX, query string, wantRows int64, args ...any) error {
	if executor, ok := db.(database.ResultExecutor); ok {
		result, err := executor.ExecResult(ctx, query, args...)
		if err != nil {
			return err
		}
		if result.RowsAffected() != wantRows {
			return ErrPublishClaimLost
		}
		return nil
	}
	return db.Exec(ctx, query, args...)
}

func entryFromMap(row map[string]any) (Entry, error) {
	raw, err := base64.StdEncoding.DecodeString(asString(row["envelope_base64"]))
	if err != nil {
		return Entry{}, err
	}
	md := map[string]any{}
	if rawMetadata := strings.TrimSpace(asString(row["metadata_json"])); rawMetadata != "" {
		if err := json.Unmarshal([]byte(rawMetadata), &md); err != nil {
			return Entry{}, err
		}
	}
	return Entry{
		ID:               asInt64(row["id"]),
		EventID:          asString(row["event_id"]),
		EventType:        asString(row["event_type"]),
		OrganizationID:   asString(row["organization_id"]),
		CorrelationID:    asString(row["correlation_id"]),
		SchemaVersion:    asString(row["schema_version"]),
		PayloadEncoding:  asString(row["payload_encoding"]),
		Envelope:         raw,
		Metadata:         md,
		OccurredAt:       asTime(row["occurred_at"]),
		CreatedAt:        asTime(row["created_at"]),
		PublishAttempts:  int(asInt64(row["publish_attempts"])),
		LastPublishError: asString(row["last_publish_error"]),
	}, nil
}

func organizationID(raw map[string]any) string {
	md := metautil.FromMap(raw)
	if md.GlobalContext != nil {
		if orgID := strings.TrimSpace(md.GlobalContext.OrganizationID); orgID != "" {
			return orgID
		}
	}
	for _, key := range []string{"organization_id", "organizationId", "org_id", "orgId"} {
		if value := strings.TrimSpace(asString(raw[key])); value != "" {
			return value
		}
	}
	return ""
}

func copyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultBatchLimit
	}
	if limit > MaxBatchLimit {
		return MaxBatchLimit
	}
	return limit
}

func normalizeMaxAttempts(maxAttempts int) int {
	if maxAttempts <= 0 {
		return DefaultMaxAttempts
	}
	return maxAttempts
}

func normalizeClaimTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return DefaultClaimTTL
	}
	if ttl < time.Millisecond {
		return time.Millisecond
	}
	return ttl
}

func newClaimToken() (string, error) {
	id, err := newEventID()
	if err != nil {
		return "", err
	}
	return strings.Replace(id, "evt_", "claim_", 1), nil
}

func newEventID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	var encoded [32]byte
	hex.Encode(encoded[:], raw[:])
	return "evt_" +
		string(encoded[0:8]) + "-" +
		string(encoded[8:12]) + "-" +
		string(encoded[12:16]) + "-" +
		string(encoded[16:20]) + "-" +
		string(encoded[20:32]), nil
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func asString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case fmt.Stringer:
		return typed.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func asInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case uint64:
		if typed > uint64(^uint(0)>>1) {
			return 0
		}
		return int64(typed)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return n
	default:
		return 0
	}
}

func asTime(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC()
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, typed); err == nil {
				return parsed.UTC()
			}
		}
	}
	return time.Time{}
}
