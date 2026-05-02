package database

import (
	"context"
	"time"
)

// RowScanner abstracts row scanning behavior for query responses.
type RowScanner interface {
	Scan(dest ...any) error
}

// DBTX is the minimal database contract shared by services and transactional helpers.
type DBTX interface {
	Exec(context.Context, string, ...any) error
	QueryRow(context.Context, string, ...any) RowScanner
	QueryMaps(context.Context, string, ...any) ([]map[string]any, error)
}

// Tx is the minimal transaction contract used by atomic application flows.
type Tx interface {
	DBTX
	Commit(context.Context) error
	Rollback(context.Context) error
}

// TxBeginner exposes transaction start support for runtime stores that can
// provide atomic SQL semantics.
type TxBeginner interface {
	BeginTx(context.Context) (Tx, error)
}

// DomainRecord is the canonical persisted record format for domain services.
type DomainRecord struct {
	Domain         string         `json:"domain"`
	Collection     string         `json:"collection"`
	OrganizationID string         `json:"organization_id"`
	RecordID       string         `json:"record_id"`
	Data           map[string]any `json:"data"`
	Vector         []float32      `json:"vector,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// StateStore is a persistence abstraction used by domain services.
type StateStore interface {
	UpsertRecord(context.Context, DomainRecord) (DomainRecord, error)
	GetRecord(context.Context, string, string, string, string) (DomainRecord, bool, error)
	ListRecords(context.Context, string, string, string, map[string]any, int) ([]DomainRecord, error)
	CountRecords(context.Context, string, string, string, map[string]any) (int64, error)
	EstimateCount(ctx context.Context, domain, collection, organizationID string) (int64, error)
	DeleteRecord(context.Context, string, string, string, string) error
}

// StoreStats provides operational metrics about the database connection pool.
type StoreStats struct {
	TotalConns          int32
	IdleConns           int32
	ActiveConns         int32
	AcquireCount        int64
	AcquireDuration     time.Duration
	MaxConns            int32
	ConstructedAt       time.Time
}

// RuntimeStore is the concrete runtime persistence contract.
// It combines query primitives, state-store behavior, and lifecycle closure.
type RuntimeStore interface {
	DBTX
	StateStore
	Stats() StoreStats
	Close()
}
