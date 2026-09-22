package placement

import (
	"context"
	"math"
	"sync"

	kiterrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

const BindingSchemaVersion uint32 = 1
const MaxBindingOutputBytes uint32 = 2 << 20
const MaxBindingTimeoutMillis uint32 = 30000

var (
	ErrBindingInvalid   = &kiterrors.Error{Code: kiterrors.CodeValidation, Message: "invalid resource binding"}
	ErrBindingStale     = &kiterrors.Error{Code: kiterrors.CodeConflict, Message: "resource or binding revision is stale"}
	ErrBindingForbidden = &kiterrors.Error{Code: kiterrors.CodeForbidden, Message: "resource binding access denied"}
	ErrBindingBudget    = &kiterrors.Error{Code: kiterrors.CodeQuotaExceeded, Message: "resource binding budget exceeded"}
	ErrBindingBusy      = &kiterrors.Error{Code: kiterrors.CodeUnavailable, Message: "resource binding capacity unavailable"}
	ErrBindingExecution = &kiterrors.Error{Code: kiterrors.CodeInternal, Message: "resource binding execution failed"}
)

type BindingAction uint8

const (
	BindingPublish BindingAction = iota + 1
	BindingConfigure
	BindingExecute
)

// BindingAuthorizer checks object access using authenticated context.
type BindingAuthorizer func(context.Context, BindingAction, uint64, uint64) error

// BindingOperation reads borrowed input and writes bounded output synchronously.
// Implementations must not retain or mutate input, retain output, or perform durable side effects.
type BindingOperation func(context.Context, []byte, []byte) (uint32, error)

// BindingType connects a stable schema identifier to its native validator.
// Validators must be bounded, read-only, and safe for concurrent calls.
type BindingType struct {
	ID       uint64
	Validate func([]byte) error
}

type BindingLimits struct {
	Resources     uint32
	Bindings      uint32
	ResidentBytes uint64
	InFlight      uint32
}

type BindingSpec struct {
	// Unknown copy costs cannot satisfy a performance requirement.
	InputCopiesKnown bool
	// InputCopies counts complete input copies inside the trusted implementation.
	InputCopies        uint32
	ID                 uint64
	ResourceID         uint64
	ResourceGeneration uint64
	InputTypeID        uint64
	OutputTypeID       uint64
	MaxInputBytes      uint64
	MaxOutputBytes     uint32
	MaxConcurrency     uint32
	Operation          BindingOperation
}

type BindingStats struct {
	Resources     uint32
	Bindings      uint32
	ResidentBytes uint64
	InFlight      uint32
}

type bindingKey struct {
	tenant string
	id     uint64
}
type resourceVersion struct {
	data       []byte
	typeID     uint64
	generation uint64
	readers    uint32
	retired    bool
}
type bindingVersion struct {
	spec     BindingSpec
	revision uint64
}
type bindingSlot struct {
	current  *bindingVersion
	inFlight uint32
}

// BindingRegistry owns bounded resources and versioned native execution bindings.
// Its mutex protects admission and publication. Operations execute outside the mutex.
type BindingRegistry struct {
	mu            sync.Mutex
	id            uint64
	limits        BindingLimits
	authorize     BindingAuthorizer
	types         map[uint64]func([]byte) error
	resources     map[bindingKey]*resourceVersion
	bindings      map[bindingKey]*bindingSlot
	residentBytes uint64
	inFlight      uint32
	closed        bool
}

func NewBindingRegistry(id uint64, limits BindingLimits, authorize BindingAuthorizer, types ...BindingType) (*BindingRegistry, error) {
	if id == 0 || authorize == nil || limits.Resources == 0 || limits.Resources > 65536 ||
		limits.Bindings == 0 || limits.Bindings > 65536 || limits.ResidentBytes == 0 ||
		limits.ResidentBytes > 512<<20 || limits.InFlight == 0 || limits.InFlight > 65536 || len(types) == 0 || len(types) > 256 {
		return nil, ErrBindingInvalid
	}
	validators := make(map[uint64]func([]byte) error, len(types))
	for _, schema := range types {
		if schema.ID == 0 || schema.Validate == nil || validators[schema.ID] != nil {
			return nil, ErrBindingInvalid
		}
		validators[schema.ID] = schema.Validate
	}
	return &BindingRegistry{id: id, limits: limits, authorize: authorize, types: validators,
		resources: make(map[bindingKey]*resourceVersion), bindings: make(map[bindingKey]*bindingSlot)}, nil
}

func (r *BindingRegistry) authority(ctx context.Context, action BindingAction, resource, binding uint64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	tenant := security.GetOrganizationIDFromContext(ctx)
	if tenant == "" || len(tenant) > 256 {
		return "", ErrBindingForbidden
	}
	if err := r.authorize(ctx, action, resource, binding); err != nil {
		return "", ErrBindingForbidden
	}
	return tenant, nil
}

func mutationCorrelation(ctx context.Context) bool {
	md, ok := metadata.FromContextOK(ctx)
	return ok && len(md.CorrelationID) > 0 && len(md.CorrelationID) <= 128
}

// PublishResource copies input once and atomically publishes a new immutable generation.
// The byte budget includes the new allocation and all outstanding retired versions.
func (r *BindingRegistry) PublishResource(ctx context.Context, id, typeID, expected uint64, data []byte) (uint64, error) {
	tenant, err := r.authority(ctx, BindingPublish, id, 0)
	if err != nil {
		return 0, err
	}
	if id == 0 || typeID == 0 || len(data) == 0 || !mutationCorrelation(ctx) {
		return 0, ErrBindingInvalid
	}
	if uint64(len(data)) > r.limits.ResidentBytes {
		return 0, ErrBindingBudget
	}
	if err := validateBindingValue(r.types[typeID], data); err != nil {
		return 0, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, ErrBindingBusy
	}
	key := bindingKey{tenant, id}
	previous := r.resources[key]
	if previous == nil && expected != 0 || previous != nil && previous.generation != expected || expected == math.MaxUint64 {
		return 0, ErrBindingStale
	}
	if previous == nil && len(r.resources) >= int(r.limits.Resources) || uint64(len(data)) > r.limits.ResidentBytes-r.residentBytes {
		return 0, ErrBindingBudget
	}
	next := &resourceVersion{data: append([]byte(nil), data...), typeID: typeID, generation: expected + 1}
	r.residentBytes += uint64(len(data))
	r.resources[key] = next
	if previous != nil {
		previous.retired = true
		r.releaseRetired(previous)
	}
	return next.generation, nil
}

// Bind publishes an operation revision after validating its resource and bounds.
func (r *BindingRegistry) Bind(ctx context.Context, spec BindingSpec, expected uint64) (RuntimeBindingRequest, error) {
	tenant, err := r.authority(ctx, BindingConfigure, spec.ResourceID, spec.ID)
	if err != nil {
		return RuntimeBindingRequest{}, err
	}
	if !mutationCorrelation(ctx) || spec.ID == 0 || spec.Operation == nil || spec.InputTypeID == 0 ||
		spec.OutputTypeID == 0 || spec.MaxInputBytes == 0 || spec.MaxOutputBytes == 0 ||
		spec.MaxOutputBytes > MaxBindingOutputBytes || spec.MaxConcurrency == 0 || spec.MaxConcurrency > r.limits.InFlight ||
		r.types[spec.OutputTypeID] == nil || !spec.InputCopiesKnown || spec.InputCopies > 16 {
		return RuntimeBindingRequest{}, ErrBindingInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bindLocked(tenant, spec, expected)
}

func (r *BindingRegistry) bindLocked(tenant string, spec BindingSpec, expected uint64) (RuntimeBindingRequest, error) {
	if r.closed {
		return RuntimeBindingRequest{}, ErrBindingBusy
	}
	resource := r.resources[bindingKey{tenant, spec.ResourceID}]
	if resource == nil || resource.generation != spec.ResourceGeneration {
		return RuntimeBindingRequest{}, ErrBindingStale
	}
	if resource.typeID != spec.InputTypeID || uint64(len(resource.data)) > spec.MaxInputBytes {
		return RuntimeBindingRequest{}, ErrBindingInvalid
	}
	key := bindingKey{tenant, spec.ID}
	slot := r.bindings[key]
	if slot == nil && expected != 0 || slot != nil && slot.current.revision != expected || expected == math.MaxUint64 {
		return RuntimeBindingRequest{}, ErrBindingStale
	}
	if slot == nil {
		if len(r.bindings) >= int(r.limits.Bindings) {
			return RuntimeBindingRequest{}, ErrBindingBudget
		}
		slot = &bindingSlot{}
		r.bindings[key] = slot
	}
	slot.current = &bindingVersion{spec: spec, revision: expected + 1}
	return RuntimeBindingRequest{SchemaVersion: BindingSchemaVersion, RegistryID: r.id, BindingID: spec.ID,
		BindingRevision: expected + 1, ResourceID: spec.ResourceID, ResourceGeneration: resource.generation,
		InputTypeID: spec.InputTypeID, OutputTypeID: spec.OutputTypeID, OutputCapacity: spec.MaxOutputBytes,
		MaxInputCopyBytes: uint64(len(resource.data)) * uint64(spec.InputCopies),
		MaxTransferBytes:  uint64(spec.MaxOutputBytes) + RuntimeBindingRequestBytes + RuntimeBindingReceiptBytes, TimeoutMillis: MaxBindingTimeoutMillis}, nil
}

func (r *BindingRegistry) releaseRetired(resource *resourceVersion) {
	if resource.retired && resource.readers == 0 {
		r.residentBytes -= uint64(len(resource.data))
		resource.data = nil
	}
}

func (r *BindingRegistry) Stats() BindingStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return BindingStats{uint32(len(r.resources)), uint32(len(r.bindings)), r.residentBytes, r.inFlight}
}

// Close refuses active executions so their resource views remain valid.
func (r *BindingRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inFlight != 0 {
		return ErrBindingBusy
	}
	r.closed = true
	clear(r.resources)
	clear(r.bindings)
	r.residentBytes = 0
	return nil
}
