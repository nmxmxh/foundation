// Package bulk implements the Foundation bulk-transfer control/data-plane
// primitive for domain-owned large payload flows.
//
// The package does not register HTTP routes, websocket handlers, or product
// policy. Scaffolded applications call it from their own upload, import, export,
// media, model-artifact, or dataset handlers after applying domain auth,
// capability, quota, and content rules. Foundation supplies the bounded chunk
// streaming, object-store manifesting, idempotency, events, and tenant/correlation
// invariants.
package bulk

import (
	"context"
	"io"
	"time"

	transport "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/transport"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
)

const (
	DefaultChunkSize = int64(4 * 1024 * 1024)
	DefaultMaxChunk  = int64(64 * 1024 * 1024)
	DefaultMaxParts  = 10_000

	EncodingIdentity = "identity"
	EncodingAuto     = "auto"
	EncodingBrotli   = "br"
	EncodingZstd     = "zstd"
	EncodingGzip     = "gzip"

	StateInitiated = "initiated"
	StateCompleted = "completed"
	StateFailed    = "failed"
	StateAborted   = "aborted"
)

type ObjectStore interface {
	PutStream(context.Context, string, io.Reader, int64, objectstore.PutOptions) (objectstore.Object, error)
	GetRange(context.Context, string, int64, int64) (io.ReadCloser, objectstore.Object, error)
	Delete(context.Context, string) error
}

type PresignObjectStore interface {
	ObjectStore
	PresignPut(context.Context, string, string, time.Duration) (string, error)
}

type CacheStore interface {
	Set(context.Context, string, any, time.Duration) error
	Get(context.Context, string) ([]byte, error)
	Del(context.Context, ...string) error
}

type Options struct {
	ObjectStore      ObjectStore
	StateStore       StateStore
	Cache            CacheStore
	EventBus         EventBus
	Namespace        string
	DefaultChunkSize int64
	MaxChunkSize     int64
	MaxParts         int
	ReceiptTTL       time.Duration
	Clock            func() time.Time
}

type InitiateRequest struct {
	TransferID     string
	TotalSize      int64
	ChunkSize      int64
	MaxMemory      int64
	MaxParts       int
	ContentType    string
	Compression    string
	Deadline       time.Time
	IdempotencyKey string
	Attributes     map[string]string
}

type TransferPlan struct {
	TransferID     string            `json:"transfer_id"`
	OrganizationID string            `json:"organization_id"`
	CorrelationID  string            `json:"correlation_id"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	TotalSize      int64             `json:"total_size"`
	ChunkSize      int64             `json:"chunk_size"`
	MaxMemory      int64             `json:"max_memory"`
	MaxParts       int               `json:"max_parts"`
	ContentType    string            `json:"content_type"`
	Compression    string            `json:"compression"`
	ObjectPrefix   string            `json:"object_prefix"`
	ManifestKey    string            `json:"manifest_key"`
	State          string            `json:"state"`
	Attributes     map[string]string `json:"attributes,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	Deadline       time.Time         `json:"deadline"`
}

type PartDescriptor struct {
	PartNumber        int
	Offset            int64
	Size              int64
	ExpectedRawSHA256 string
	ContentType       string
}

type SignedPartRequest struct {
	TransferID string
	Descriptor PartDescriptor
	Expiry     time.Duration
}

type SignedPartGrant struct {
	TransferID  string            `json:"transfer_id"`
	PartNumber  int               `json:"part_number"`
	Offset      int64             `json:"offset"`
	Size        int64             `json:"size"`
	ObjectKey   string            `json:"object_key"`
	UploadURL   string            `json:"upload_url"`
	ContentType string            `json:"content_type"`
	Headers     map[string]string `json:"headers,omitempty"`
	ExpiresAt   time.Time         `json:"expires_at"`
}

type HTTPPartRequest struct {
	Envelope transport.Envelope
	Reader   io.Reader
}

type DescriptorSource interface {
	OpenDescriptor(context.Context, PartDescriptor) (io.ReadCloser, error)
}

type PartReceipt struct {
	TransferID       string    `json:"transfer_id"`
	OrganizationID   string    `json:"organization_id"`
	CorrelationID    string    `json:"correlation_id"`
	PartNumber       int       `json:"part_number"`
	Offset           int64     `json:"offset"`
	RawSize          int64     `json:"raw_size"`
	EncodedSize      int64     `json:"encoded_size"`
	RawSHA256        string    `json:"raw_sha256"`
	EncodedSHA256    string    `json:"encoded_sha256"`
	Encoding         string    `json:"encoding"`
	ObjectKey        string    `json:"object_key"`
	ObjectETag       string    `json:"object_etag,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	IdempotentReplay bool      `json:"idempotent_replay,omitempty"`
}

type CompleteRequest struct {
	ExpectedRootSHA256 string
}

type TransferManifest struct {
	TransferID     string        `json:"transfer_id"`
	OrganizationID string        `json:"organization_id"`
	CorrelationID  string        `json:"correlation_id"`
	TotalSize      int64         `json:"total_size"`
	ChunkSize      int64         `json:"chunk_size"`
	ContentType    string        `json:"content_type"`
	Compression    string        `json:"compression"`
	RootSHA256     string        `json:"root_sha256"`
	ManifestKey    string        `json:"manifest_key"`
	Parts          []PartReceipt `json:"parts"`
	CompletedAt    time.Time     `json:"completed_at"`
}

type MissingPart struct {
	PartNumber        int    `json:"part_number"`
	Offset            int64  `json:"offset"`
	Size              int64  `json:"size"`
	ExpectedRawSHA256 string `json:"expected_raw_sha256,omitempty"`
}

type TransferStatus struct {
	TransferID     string        `json:"transfer_id"`
	OrganizationID string        `json:"organization_id"`
	CorrelationID  string        `json:"correlation_id"`
	State          string        `json:"state"`
	TotalSize      int64         `json:"total_size"`
	ChunkSize      int64         `json:"chunk_size"`
	BytesAccepted  int64         `json:"bytes_accepted"`
	PartsAccepted  int           `json:"parts_accepted"`
	MissingParts   []MissingPart `json:"missing_parts,omitempty"`
	ManifestKey    string        `json:"manifest_key,omitempty"`
	RootSHA256     string        `json:"root_sha256,omitempty"`
	ResumeToken    string        `json:"resume_token"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type LaneDiagnostics struct {
	Ingress            string            `json:"ingress"`
	ObjectStoreBackend string            `json:"object_store_backend"`
	ChunkSize          int64             `json:"chunk_size"`
	Compression        string            `json:"compression"`
	CopyBudget         string            `json:"copy_budget"`
	MemoryBudget       int64             `json:"memory_budget"`
	ResumeSupported    bool              `json:"resume_supported"`
	DistributedState   bool              `json:"distributed_state"`
	ZeroCopyAvailable  bool              `json:"zero_copy_available"`
	MPTCPAvailable     bool              `json:"mptcp_available"`
	QUICAvailable      bool              `json:"quic_available"`
	KernelPacing       bool              `json:"kernel_pacing"`
	DeadlineRisk       string            `json:"deadline_risk"`
	Fallback           string            `json:"fallback,omitempty"`
	Attributes         map[string]string `json:"attributes,omitempty"`
}

type LaneKind string

const (
	LaneHTTPStream        LaneKind = "http_stream"
	LaneSignedObjectStore LaneKind = "signed_objectstore"
	LaneDescriptor        LaneKind = "descriptor"
	LaneKernelZeroCopy    LaneKind = "kernel_zero_copy"
	LaneMPTCP             LaneKind = "mptcp"
	LaneQUIC              LaneKind = "quic"
)

type LaneRequest struct {
	Plan                 TransferPlan
	Part                 PartDescriptor
	Locality             string
	TrustedProducer      bool
	DirectObjectStore    bool
	DescriptorAvailable  bool
	HTTPStreamAvailable  bool
	QUICAdapterAvailable bool
	KernelAdapterEnabled bool
	Capabilities         PlatformCapabilities
}

type LaneCandidate struct {
	Kind      LaneKind `json:"kind"`
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
}

type LanePlan struct {
	Selected   LaneKind        `json:"selected"`
	Candidates []LaneCandidate `json:"candidates"`
	Diagnostic LaneDiagnostics `json:"diagnostic"`
}

type PipelineOptions struct {
	Manager            *Manager
	Ingress            string
	ObjectStoreBackend string
	DistributedState   bool
	ZeroCopyAvailable  bool
	MPTCPAvailable     bool
	QUICAvailable      bool
	KernelPacing       bool
	Attributes         map[string]string
	Clock              func() time.Time
}

type RangeDescriptor struct {
	TransferID string
	Offset     int64
	Length     int64
	Parts      []int
}

type RangePart struct {
	TransferID string
	PartNumber int
	Offset     int64
	Length     int64
	Reader     io.ReadCloser
}
