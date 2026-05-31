package hermes

import (
	"errors"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
)

const (
	defaultMaxRecords       = 100000
	defaultMaxBytes         = 64 << 20
	defaultMaxTombstones    = 10000
	defaultMaxAppliedEvents = 100000
)

var (
	ErrProjectionNotFound = errors.New("hermes projection not found")
	ErrProjectionLimit    = errors.New("hermes projection limit reached")
	ErrProjectionBusy     = errors.New("hermes projection publish in progress")
	ErrInvalidProjection  = errors.New("hermes projection is invalid")
	ErrInvalidEvent       = errors.New("hermes event is invalid")
	ErrFenceNotSatisfied  = errors.New("hermes epoch fence not satisfied")
)

type Operation string

const (
	OperationUpsert Operation = "upsert"
	OperationDelete Operation = "delete"
)

// ProjectionSpec defines a bounded hot subset over canonical domain records.
type ProjectionSpec struct {
	Name             string
	Domain           string
	Collection       string
	IndexedFields    []string
	MaxRecords       int
	MaxBytes         int64
	MaxIndexes       int
	MaxTombstones    int
	MaxAppliedEvents int
	Freshness        time.Duration
}

// Event is the durable mutation shape consumed by the Hermes hot plane.
type Event struct {
	Operation     Operation
	SourceID      string
	Version       uint64
	CorrelationID string
	Record        database.DomainRecord
}

// Fence lets callers require a minimum published epoch before a read succeeds.
type Fence struct {
	MinEpoch uint64
}

type Query struct {
	OrganizationID string
	Filters        map[string]any
	Limit          int
}

type ApplyResult struct {
	Epoch      uint64
	Applied    int
	Duplicates int
	Ignored    int
}

type Stats struct {
	Projection       string
	Epoch            uint64
	SourceWatermark  uint64
	Records          int
	ApproxBytes      int64
	Tombstones       int
	AppliedEvents    int
	RejectedApplies  int64
	IndexCompactions int64
	MaxRecords       int
	MaxBytes         int64
	MaxTombstones    int
	MaxAppliedEvents int
}

type RuntimeStats struct {
	Projections    []Stats
	Fallbacks      int64
	DegradedScopes int64
}

// RecordView is a borrowed, zero-copy read view valid only during callbacks.
type RecordView struct {
	Domain         string
	Collection     string
	OrganizationID string
	RecordID       string
	Data           map[string]any
	Vector         []float32
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Version        uint64
	Epoch          uint64
}

type recordEntry struct {
	record  database.DomainRecord
	source  string
	version uint64
	bytes   int64
}

type tombstoneEntry struct {
	version uint64
	source  string
}

type recordScope struct {
	domain         string
	collection     string
	organizationID string
}

type fieldIndex struct {
	scope recordScope
	field string
	kind  byte
	value string
}

type recordOrderEntry struct {
	key     string
	version uint64
}
