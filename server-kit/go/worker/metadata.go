package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// JobMetadata stores extended context for queued workflows.
type JobMetadata struct {
	JobID         int64          `json:"job_id"`
	WorkflowName  string         `json:"workflow_name"`
	EntityType    string         `json:"entity_type"`
	EntityID      string         `json:"entity_id"`
	UserID        string         `json:"user_id"`
	CorrelationID string         `json:"correlation_id"`
	RawPayload    []byte         `json:"raw_payload"`
	TrackingData  map[string]any `json:"tracking_data"`
}

// MetadataStore is a minimal metadata persistence abstraction.
type MetadataStore interface {
	Save(context.Context, JobMetadata) error
	Get(context.Context, int64) (JobMetadata, error)
	UpdateTrackingData(context.Context, int64, map[string]any) error
}

// InMemoryMetadataStore is the local implementation for development/test.
type InMemoryMetadataStore struct {
	mu    sync.RWMutex
	store map[int64]JobMetadata
}

func NewInMemoryMetadataStore() *InMemoryMetadataStore {
	return &InMemoryMetadataStore{store: map[int64]JobMetadata{}}
}

func (s *InMemoryMetadataStore) Save(_ context.Context, metadata JobMetadata) error {
	if metadata.JobID <= 0 {
		return fmt.Errorf("job_id is required")
	}
	if metadata.WorkflowName == "" {
		return fmt.Errorf("workflow_name is required")
	}
	if metadata.EntityType == "" {
		return fmt.Errorf("entity_type is required")
	}
	if metadata.EntityID == "" {
		return fmt.Errorf("entity_id is required")
	}
	if metadata.TrackingData == nil {
		metadata.TrackingData = map[string]any{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store[metadata.JobID] = metadata
	return nil
}

func (s *InMemoryMetadataStore) Get(_ context.Context, jobID int64) (JobMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	metadata, ok := s.store[jobID]
	if !ok {
		return JobMetadata{}, fmt.Errorf("job metadata not found for %d", jobID)
	}
	return metadata, nil
}

func (s *InMemoryMetadataStore) UpdateTrackingData(_ context.Context, jobID int64, trackingData map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	metadata, ok := s.store[jobID]
	if !ok {
		return fmt.Errorf("job metadata not found for %d", jobID)
	}
	metadata.TrackingData = trackingData
	s.store[jobID] = metadata
	return nil
}

func (m JobMetadata) ToJSON() ([]byte, error) {
	return json.Marshal(m)
}

// PostgresMetadataStore implements MetadataStore using pgxpool.Pool.
type PostgresMetadataStore struct {
	pool *pgxpool.Pool
}

func NewPostgresMetadataStore(pool *pgxpool.Pool) *PostgresMetadataStore {
	return &PostgresMetadataStore{pool: pool}
}

func (s *PostgresMetadataStore) Save(ctx context.Context, m JobMetadata) error {
	query := `
		INSERT INTO river_job_metadata (
			job_id, workflow_name, entity_type, entity_id, user_id, correlation_id, raw_payload, tracking_data
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (job_id) DO UPDATE SET
			workflow_name = EXCLUDED.workflow_name,
			entity_type = EXCLUDED.entity_type,
			entity_id = EXCLUDED.entity_id,
			user_id = EXCLUDED.user_id,
			correlation_id = EXCLUDED.correlation_id,
			raw_payload = EXCLUDED.raw_payload,
			tracking_data = EXCLUDED.tracking_data,
			updated_at = now()
	`
	_, err := s.pool.Exec(ctx, query,
		m.JobID, m.WorkflowName, m.EntityType, m.EntityID, m.UserID, m.CorrelationID, m.RawPayload, m.TrackingData,
	)
	return err
}

func (s *PostgresMetadataStore) Get(ctx context.Context, jobID int64) (JobMetadata, error) {
	query := `
		SELECT job_id, workflow_name, entity_type, entity_id, user_id, correlation_id, raw_payload, tracking_data
		FROM river_job_metadata
		WHERE job_id = $1
	`
	var m JobMetadata
	err := s.pool.QueryRow(ctx, query, jobID).Scan(
		&m.JobID, &m.WorkflowName, &m.EntityType, &m.EntityID, &m.UserID, &m.CorrelationID, &m.RawPayload, &m.TrackingData,
	)
	if err != nil {
		return JobMetadata{}, err
	}
	return m, nil
}

func (s *PostgresMetadataStore) UpdateTrackingData(ctx context.Context, jobID int64, trackingData map[string]any) error {
	query := `UPDATE river_job_metadata SET tracking_data = $1, updated_at = now() WHERE job_id = $2`
	_, err := s.pool.Exec(ctx, query, trackingData, jobID)
	return err
}
