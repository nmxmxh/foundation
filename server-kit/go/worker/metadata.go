package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// JobMetadata stores extended context for queued workflows.
type JobMetadata struct {
	JobID         int64          `json:"job_id"`
	WorkflowName  string         `json:"workflow_name"`
	EntityType    string         `json:"entity_type"`
	EntityID      string         `json:"entity_id"`
	UserID        string         `json:"user_id"`
	CorrelationID string         `json:"correlation_id"`
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
