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
	Domain         string
	Collection     string
	OrganizationID string
	RecordID       string
	Data           map[string]any
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// StateStore is a persistence abstraction used by domain services.
type StateStore interface {
	UpsertRecord(context.Context, DomainRecord) (DomainRecord, error)
	GetRecord(context.Context, string, string, string, string) (DomainRecord, bool, error)
	ListRecords(context.Context, string, string, string, map[string]any, int) ([]DomainRecord, error)
	CountRecords(context.Context, string, string, string, map[string]any) (int64, error)
}

// RuntimeStore is the concrete runtime persistence contract.
// It combines query primitives, state-store behavior, and lifecycle closure.
type RuntimeStore interface {
	DBTX
	StateStore
	Close()
}
