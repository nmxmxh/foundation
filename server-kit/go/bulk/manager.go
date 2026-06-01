package bulk

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	apperrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

var transferIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

const maxInt64 = int64(1<<63 - 1)

type EventBus interface {
	Publish(context.Context, events.Envelope) error
}

type Manager struct {
	objects          ObjectStore
	state            StateStore
	cache            CacheStore
	events           EventBus
	namespace        string
	defaultChunkSize int64
	maxChunkSize     int64
	maxParts         int
	receiptTTL       time.Duration
	clock            func() time.Time
}

func NewManager(opts Options) (*Manager, error) {
	if opts.ObjectStore == nil {
		return nil, apperrors.New(apperrors.CodeValidation, "object store is required")
	}
	if opts.StateStore == nil {
		opts.StateStore = NewMemoryStateStore()
	}
	opts.Namespace = strings.Trim(opts.Namespace, "/ ")
	if opts.Namespace == "" {
		opts.Namespace = "bulk"
	}
	if opts.DefaultChunkSize <= 0 {
		opts.DefaultChunkSize = DefaultChunkSize
	}
	if opts.MaxChunkSize <= 0 {
		opts.MaxChunkSize = DefaultMaxChunk
	}
	if opts.MaxParts <= 0 {
		opts.MaxParts = DefaultMaxParts
	}
	if opts.ReceiptTTL <= 0 {
		opts.ReceiptTTL = time.Hour
	}
	if opts.Clock == nil {
		opts.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &Manager{
		objects:          opts.ObjectStore,
		state:            opts.StateStore,
		cache:            opts.Cache,
		events:           opts.EventBus,
		namespace:        opts.Namespace,
		defaultChunkSize: opts.DefaultChunkSize,
		maxChunkSize:     opts.MaxChunkSize,
		maxParts:         opts.MaxParts,
		receiptTTL:       opts.ReceiptTTL,
		clock:            opts.Clock,
	}, nil
}

func (m *Manager) Initiate(ctx context.Context, req InitiateRequest) (TransferPlan, error) {
	orgID, md, err := m.contextScope(ctx)
	if err != nil {
		return TransferPlan{}, err
	}
	plan, err := m.buildPlan(req, orgID, md)
	if err != nil {
		m.emitFailure(ctx, "bulk:transfer:initiate:v1:failed", "", err)
		return TransferPlan{}, err
	}
	if existing, err := m.state.LoadPlan(ctx, orgID, plan.TransferID); err == nil {
		return m.replayInitiate(ctx, existing, plan)
	}
	if m.events != nil {
		if err := m.emit(ctx, "bulk:transfer:initiate:v1:requested", planPayload(plan)); err != nil {
			return TransferPlan{}, err
		}
	}
	if err := m.state.SavePlan(ctx, plan); err != nil {
		m.emitFailure(ctx, "bulk:transfer:initiate:v1:failed", plan.TransferID, err)
		return TransferPlan{}, apperrors.Wrap(err, apperrors.CodeDependency, "save transfer plan")
	}
	if m.events != nil {
		if err := m.emit(ctx, "bulk:transfer:initiate:v1:success", planPayload(plan)); err != nil {
			return TransferPlan{}, err
		}
	}
	m.cacheProgress(ctx, plan, 0, StateInitiated)
	return plan, nil
}

func (m *Manager) AcceptPart(ctx context.Context, transferID string, desc PartDescriptor, reader io.Reader) (PartReceipt, error) {
	plan, err := m.loadPlanForMutation(ctx, transferID, "bulk:part:accept:v1:failed")
	if err != nil {
		return PartReceipt{}, err
	}
	if existing, err := m.state.LoadReceipt(ctx, plan.OrganizationID, plan.TransferID, desc.PartNumber); err == nil {
		return m.replayPart(existing, desc)
	}
	if err := m.validatePart(plan, desc, reader); err != nil {
		m.emitFailure(ctx, "bulk:part:accept:v1:failed", plan.TransferID, err)
		return PartReceipt{}, err
	}
	if m.events != nil {
		if err := m.emit(ctx, "bulk:part:accept:v1:requested", partPayload(plan, desc)); err != nil {
			return PartReceipt{}, err
		}
	}
	receipt, err := m.storePart(ctx, plan, desc, reader)
	if err != nil {
		m.emitFailure(ctx, "bulk:part:accept:v1:failed", plan.TransferID, err)
		return PartReceipt{}, err
	}
	if err := m.state.SaveReceipt(ctx, receipt); err != nil {
		_ = m.objects.Delete(ctx, receipt.ObjectKey)
		return PartReceipt{}, apperrors.Wrap(err, apperrors.CodeDependency, "save part receipt")
	}
	m.cacheReceipt(ctx, receipt)
	if m.events != nil {
		if err := m.emit(ctx, "bulk:part:accept:v1:success", receiptPayload(receipt)); err != nil {
			return PartReceipt{}, err
		}
	}
	return receipt, nil
}

func (m *Manager) GrantSignedPart(ctx context.Context, req SignedPartRequest) (SignedPartGrant, error) {
	plan, err := m.loadPlanForMutation(ctx, req.TransferID, "bulk:part:signed_grant:v1:failed")
	if err != nil {
		return SignedPartGrant{}, err
	}
	if err := m.validatePart(plan, req.Descriptor, strings.NewReader("")); err != nil {
		m.emitFailure(ctx, "bulk:part:signed_grant:v1:failed", plan.TransferID, err)
		return SignedPartGrant{}, err
	}
	signer, ok := m.objects.(PresignObjectStore)
	if !ok {
		err := apperrors.New(apperrors.CodeNotImplemented, "object store does not support signed part uploads")
		m.emitFailure(ctx, "bulk:part:signed_grant:v1:failed", plan.TransferID, err)
		return SignedPartGrant{}, err
	}
	expiry := req.Expiry
	if expiry <= 0 || expiry > m.receiptTTL {
		expiry = m.receiptTTL
	}
	key := partObjectKey(plan, req.Descriptor.PartNumber)
	contentType := firstNonEmpty(req.Descriptor.ContentType, plan.ContentType, "application/octet-stream")
	url, err := signer.PresignPut(ctx, key, contentType, expiry)
	if err != nil {
		err = apperrors.Wrap(err, apperrors.CodeDependency, "presign transfer part")
		m.emitFailure(ctx, "bulk:part:signed_grant:v1:failed", plan.TransferID, err)
		return SignedPartGrant{}, err
	}
	return SignedPartGrant{
		TransferID:  plan.TransferID,
		PartNumber:  req.Descriptor.PartNumber,
		Offset:      req.Descriptor.Offset,
		Size:        req.Descriptor.Size,
		ObjectKey:   key,
		UploadURL:   url,
		ContentType: contentType,
		Headers: map[string]string{
			"content-type": contentType,
		},
		ExpiresAt: m.clock().Add(expiry),
	}, nil
}

func (m *Manager) AcceptSignedPart(ctx context.Context, transferID string, desc PartDescriptor) (PartReceipt, error) {
	plan, err := m.loadPlanForMutation(ctx, transferID, "bulk:part:signed_accept:v1:failed")
	if err != nil {
		return PartReceipt{}, err
	}
	if existing, err := m.state.LoadReceipt(ctx, plan.OrganizationID, plan.TransferID, desc.PartNumber); err == nil {
		return m.replayPart(existing, desc)
	}
	if err := m.validatePart(plan, desc, strings.NewReader("")); err != nil {
		m.emitFailure(ctx, "bulk:part:signed_accept:v1:failed", plan.TransferID, err)
		return PartReceipt{}, err
	}
	receipt, err := m.verifySignedPart(ctx, plan, desc)
	if err != nil {
		m.emitFailure(ctx, "bulk:part:signed_accept:v1:failed", plan.TransferID, err)
		return PartReceipt{}, err
	}
	if err := m.state.SaveReceipt(ctx, receipt); err != nil {
		return PartReceipt{}, apperrors.Wrap(err, apperrors.CodeDependency, "save signed part receipt")
	}
	m.cacheReceipt(ctx, receipt)
	if m.events != nil {
		if err := m.emit(ctx, "bulk:part:signed_accept:v1:success", receiptPayload(receipt)); err != nil {
			return PartReceipt{}, err
		}
	}
	return receipt, nil
}

func (m *Manager) Complete(ctx context.Context, transferID string, req CompleteRequest) (TransferManifest, error) {
	plan, err := m.loadPlanForMutation(ctx, transferID, "bulk:transfer:complete:v1:failed")
	if err != nil {
		return TransferManifest{}, err
	}
	if m.events != nil {
		if err := m.emit(ctx, "bulk:transfer:complete:v1:requested", planPayload(plan)); err != nil {
			return TransferManifest{}, err
		}
	}
	receipts, err := m.state.ListReceipts(ctx, plan.OrganizationID, plan.TransferID)
	if err != nil {
		return TransferManifest{}, apperrors.Wrap(err, apperrors.CodeDependency, "list part receipts")
	}
	manifest, err := m.buildManifest(plan, receipts, req.ExpectedRootSHA256)
	if err != nil {
		m.emitFailure(ctx, "bulk:transfer:complete:v1:failed", plan.TransferID, err)
		return TransferManifest{}, err
	}
	if err := m.persistManifest(ctx, manifest); err != nil {
		m.emitFailure(ctx, "bulk:transfer:complete:v1:failed", plan.TransferID, err)
		return TransferManifest{}, err
	}
	plan.State = StateCompleted
	_ = m.state.SavePlan(ctx, plan)
	m.cacheProgress(ctx, plan, manifest.TotalSize, StateCompleted)
	if m.events != nil {
		if err := m.emit(ctx, "bulk:transfer:complete:v1:success", manifestPayload(manifest)); err != nil {
			return TransferManifest{}, err
		}
	}
	return manifest, nil
}

func (m *Manager) OpenRange(ctx context.Context, transferID string, offset, length int64) (io.ReadCloser, RangeDescriptor, error) {
	plan, err := m.loadPlanForRead(ctx, transferID)
	if err != nil {
		return nil, RangeDescriptor{}, err
	}
	manifest, err := m.state.LoadManifest(ctx, plan.OrganizationID, plan.TransferID)
	if err != nil {
		return nil, RangeDescriptor{}, apperrors.Wrap(err, apperrors.CodeNotFound, "manifest is not available")
	}
	readers, partNumbers, err := m.openIdentityRange(ctx, manifest, offset, length)
	if err != nil {
		return nil, RangeDescriptor{}, err
	}
	return multiReadCloser(readers), RangeDescriptor{
		TransferID: transferID,
		Offset:     offset,
		Length:     length,
		Parts:      partNumbers,
	}, nil
}

func (m *Manager) ForEachRange(ctx context.Context, transferID string, offset, length int64, fn func(RangePart) error) (RangeDescriptor, error) {
	if fn == nil {
		return RangeDescriptor{}, apperrors.New(apperrors.CodeValidation, "range callback is required")
	}
	plan, err := m.loadPlanForRead(ctx, transferID)
	if err != nil {
		return RangeDescriptor{}, err
	}
	manifest, err := m.state.LoadManifest(ctx, plan.OrganizationID, plan.TransferID)
	if err != nil {
		return RangeDescriptor{}, apperrors.Wrap(err, apperrors.CodeNotFound, "manifest is not available")
	}
	return m.forEachIdentityRange(ctx, manifest, offset, length, fn)
}

func (m *Manager) buildPlan(req InitiateRequest, orgID string, md metadata.EnvelopeMetadata) (TransferPlan, error) {
	now := m.clock()
	chunkSize := firstPositive(req.ChunkSize, m.defaultChunkSize)
	maxMemory := firstPositive(req.MaxMemory, chunkSize)
	maxParts := req.MaxParts
	if maxParts <= 0 {
		maxParts = m.maxParts
	}
	if err := validateInitiate(req, chunkSize, maxMemory, maxParts, m.maxChunkSize, now); err != nil {
		return TransferPlan{}, err
	}
	transferID := strings.TrimSpace(req.TransferID)
	if transferID == "" {
		transferID = newTransferID(now)
	}
	if !transferIDPattern.MatchString(transferID) {
		return TransferPlan{}, apperrors.New(apperrors.CodeValidation, "transfer_id has invalid format")
	}
	contentType := strings.TrimSpace(req.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	compression := normalizeEncoding(req.Compression)
	prefix := fmt.Sprintf("%s/%s/%s", m.namespace, orgID, transferID)
	return TransferPlan{
		TransferID:     transferID,
		OrganizationID: orgID,
		CorrelationID:  md.EnsureCorrelation(),
		IdempotencyKey: firstNonEmpty(req.IdempotencyKey, md.IdempotencyKey),
		TotalSize:      req.TotalSize,
		ChunkSize:      chunkSize,
		MaxMemory:      maxMemory,
		MaxParts:       maxParts,
		ContentType:    contentType,
		Compression:    compression,
		ObjectPrefix:   prefix,
		ManifestKey:    prefix + "/manifest.json",
		State:          StateInitiated,
		Attributes:     cloneStringMap(req.Attributes),
		CreatedAt:      now,
		Deadline:       req.Deadline,
	}, nil
}

func validateInitiate(req InitiateRequest, chunkSize, maxMemory int64, maxParts int, maxChunkSize int64, now time.Time) error {
	if req.TotalSize < 0 {
		return apperrors.New(apperrors.CodeValidation, "total_size must be non-negative")
	}
	if chunkSize <= 0 || chunkSize > maxChunkSize {
		return apperrors.New(apperrors.CodeValidation, "chunk_size is outside allowed bounds")
	}
	if maxMemory <= 0 || maxMemory > maxChunkSize {
		return apperrors.New(apperrors.CodeValidation, "max_memory is outside allowed bounds")
	}
	if chunkSize > maxMemory {
		return apperrors.New(apperrors.CodeValidation, "chunk_size cannot exceed max_memory")
	}
	if maxParts <= 0 || maxParts > DefaultMaxParts {
		return apperrors.New(apperrors.CodeValidation, "max_parts is outside allowed bounds")
	}
	if req.TotalSize > 0 && ceilDiv(req.TotalSize, chunkSize) > int64(maxParts) {
		return apperrors.New(apperrors.CodeQuotaExceeded, "transfer exceeds part budget")
	}
	if !req.Deadline.IsZero() && !req.Deadline.After(now) {
		return apperrors.New(apperrors.CodeExpired, "deadline has already expired")
	}
	if enc := strings.TrimSpace(req.Compression); enc != "" && normalizeEncoding(enc) == "" {
		return apperrors.New(apperrors.CodeValidation, "unsupported compression")
	}
	return nil
}

func (m *Manager) contextScope(ctx context.Context) (string, metadata.EnvelopeMetadata, error) {
	md := metadata.FromContext(ctx)
	md.EnsureCorrelation()
	authOrg := strings.TrimSpace(security.GetOrganizationIDFromContext(ctx))
	if authOrg == "" {
		return "", md, apperrors.New(apperrors.CodeUnauthorized, "organization context is required")
	}
	if md.GlobalContext != nil && strings.TrimSpace(md.GlobalContext.OrganizationID) != "" {
		if strings.TrimSpace(md.GlobalContext.OrganizationID) != authOrg {
			return "", md, apperrors.New(apperrors.CodeForbidden, "metadata organization does not match authenticated context")
		}
	}
	return authOrg, md, nil
}

func (m *Manager) loadPlanForMutation(ctx context.Context, transferID, failedEvent string) (TransferPlan, error) {
	plan, err := m.loadPlanForRead(ctx, transferID)
	if err != nil {
		m.emitFailure(ctx, failedEvent, transferID, err)
		return TransferPlan{}, err
	}
	if plan.State != StateInitiated {
		err := apperrors.New(apperrors.CodeInvalidState, "transfer is not accepting mutations")
		m.emitFailure(ctx, failedEvent, transferID, err)
		return TransferPlan{}, err
	}
	if !plan.Deadline.IsZero() && !m.clock().Before(plan.Deadline) {
		err := apperrors.New(apperrors.CodeExpired, "transfer deadline expired")
		m.emitFailure(ctx, failedEvent, transferID, err)
		return TransferPlan{}, err
	}
	return plan, nil
}

func (m *Manager) loadPlanForRead(ctx context.Context, transferID string) (TransferPlan, error) {
	orgID, _, err := m.contextScope(ctx)
	if err != nil {
		return TransferPlan{}, err
	}
	transferID = strings.TrimSpace(transferID)
	if transferID == "" {
		return TransferPlan{}, apperrors.New(apperrors.CodeValidation, "transfer_id is required")
	}
	plan, err := m.state.LoadPlan(ctx, orgID, transferID)
	if err != nil {
		return TransferPlan{}, apperrors.Wrap(err, apperrors.CodeNotFound, "transfer was not found")
	}
	return plan, nil
}

func (m *Manager) replayInitiate(ctx context.Context, existing, candidate TransferPlan) (TransferPlan, error) {
	if existing.OrganizationID != candidate.OrganizationID {
		return TransferPlan{}, apperrors.New(apperrors.CodeForbidden, "transfer belongs to another organization")
	}
	if existing.IdempotencyKey != "" && existing.IdempotencyKey == candidate.IdempotencyKey {
		m.cacheProgress(ctx, existing, 0, existing.State)
		return existing, nil
	}
	return TransferPlan{}, apperrors.New(apperrors.CodeConflict, "transfer_id already exists")
}

func (m *Manager) replayPart(existing PartReceipt, desc PartDescriptor) (PartReceipt, error) {
	if desc.ExpectedRawSHA256 != "" && !strings.EqualFold(desc.ExpectedRawSHA256, existing.RawSHA256) {
		return PartReceipt{}, apperrors.New(apperrors.CodeConflict, "part replay hash does not match existing receipt")
	}
	if desc.Size >= 0 && desc.Size != existing.RawSize {
		return PartReceipt{}, apperrors.New(apperrors.CodeConflict, "part replay size does not match existing receipt")
	}
	existing.IdempotentReplay = true
	return existing, nil
}

func (m *Manager) validatePart(plan TransferPlan, desc PartDescriptor, reader io.Reader) error {
	if reader == nil {
		return apperrors.New(apperrors.CodeValidation, "part reader is required")
	}
	if desc.PartNumber < 0 || desc.PartNumber >= plan.MaxParts {
		return apperrors.New(apperrors.CodeValidation, "part_number is outside allowed bounds")
	}
	if desc.Offset < 0 || desc.Size < 0 {
		return apperrors.New(apperrors.CodeValidation, "part offset and size must be non-negative")
	}
	if desc.Size > plan.ChunkSize || desc.Size > plan.MaxMemory {
		return apperrors.New(apperrors.CodeQuotaExceeded, "part exceeds chunk or memory budget")
	}
	partEnd, ok := checkedAddInt64(desc.Offset, desc.Size)
	if !ok {
		return apperrors.New(apperrors.CodeValidation, "part offset and size overflow")
	}
	if plan.TotalSize >= 0 && partEnd > plan.TotalSize {
		return apperrors.New(apperrors.CodeValidation, "part exceeds transfer total_size")
	}
	if !validSHA256(desc.ExpectedRawSHA256) {
		return apperrors.New(apperrors.CodeValidation, "expected raw sha256 is required")
	}
	return nil
}

func (m *Manager) storePart(ctx context.Context, plan TransferPlan, desc PartDescriptor, reader io.Reader) (PartReceipt, error) {
	switch plan.Compression {
	case EncodingAuto:
		return m.storeAutoPart(ctx, plan, desc, reader)
	case EncodingBrotli:
		return m.storeCompressedPart(ctx, plan, desc, reader, EncodingBrotli)
	case EncodingZstd:
		return m.storeCompressedPart(ctx, plan, desc, reader, EncodingZstd)
	case EncodingGzip:
		return m.storeCompressedPart(ctx, plan, desc, reader, EncodingGzip)
	default:
		return m.storeIdentityPart(ctx, plan, desc, reader)
	}
}

func (m *Manager) storeIdentityPart(ctx context.Context, plan TransferPlan, desc PartDescriptor, reader io.Reader) (PartReceipt, error) {
	key := partObjectKey(plan, desc.PartNumber)
	raw := newHashingReader(newBoundedReader(reader, desc.Size))
	object, err := m.objects.PutStream(ctx, key, raw, desc.Size, partPutOptions(plan, desc, EncodingIdentity))
	if err != nil {
		_ = m.objects.Delete(ctx, key)
		return PartReceipt{}, storePartError(err, "store transfer part")
	}
	digest := raw.SumHex()
	if err := verifyPartDigest(desc, raw.N(), digest); err != nil {
		_ = m.objects.Delete(ctx, key)
		return PartReceipt{}, err
	}
	return newReceipt(plan, desc, object, raw.N(), raw.N(), digest, digest, EncodingIdentity, m.clock()), nil
}

func (m *Manager) storeCompressedPart(ctx context.Context, plan TransferPlan, desc PartDescriptor, reader io.Reader, encoding string) (PartReceipt, error) {
	key := partObjectKey(plan, desc.PartNumber)
	encoded, encodedHash, rawResult, err := compressedPartReader(reader, desc.Size, encoding)
	if err != nil {
		return PartReceipt{}, err
	}
	object, err := m.objects.PutStream(ctx, key, encoded, -1, partPutOptions(plan, desc, encoding))
	if err != nil {
		_ = encoded.CloseWithError(err)
		<-rawResult
		_ = m.objects.Delete(ctx, key)
		return PartReceipt{}, storePartError(err, "store compressed transfer part")
	}
	result := <-rawResult
	if result.err != nil {
		_ = m.objects.Delete(ctx, key)
		return PartReceipt{}, result.err
	}
	if err := verifyPartDigest(desc, result.rawSize, result.rawSHA256); err != nil {
		_ = m.objects.Delete(ctx, key)
		return PartReceipt{}, err
	}
	return newReceipt(plan, desc, object, result.rawSize, encodedHash.N(), result.rawSHA256, encodedHash.SumHex(), encoding, m.clock()), nil
}

func (m *Manager) storeAutoPart(ctx context.Context, plan TransferPlan, desc PartDescriptor, reader io.Reader) (PartReceipt, error) {
	payload, digest, err := readBoundedPart(reader, desc.Size)
	if err != nil {
		return PartReceipt{}, err
	}
	if err := verifyPartDigest(desc, int64(len(payload)), digest); err != nil {
		return PartReceipt{}, err
	}
	encoded, encoding, err := compressAuto(payload)
	if err != nil {
		return PartReceipt{}, storePartError(err, "compress transfer part")
	}
	key := partObjectKey(plan, desc.PartNumber)
	object, err := m.objects.PutStream(ctx, key, bytes.NewReader(encoded), int64(len(encoded)), partPutOptions(plan, desc, encoding))
	if err != nil {
		_ = m.objects.Delete(ctx, key)
		return PartReceipt{}, storePartError(err, "store auto-compressed transfer part")
	}
	encodedDigest := digest
	if encoding != EncodingIdentity {
		encodedDigest = sha256Hex(encoded)
	}
	return newReceipt(plan, desc, object, int64(len(payload)), int64(len(encoded)), digest, encodedDigest, encoding, m.clock()), nil
}

func (m *Manager) verifySignedPart(ctx context.Context, plan TransferPlan, desc PartDescriptor) (PartReceipt, error) {
	if plan.Compression != EncodingIdentity {
		return PartReceipt{}, apperrors.New(apperrors.CodeNotImplemented, "signed part verification requires identity encoding")
	}
	key := partObjectKey(plan, desc.PartNumber)
	reader, object, err := m.objects.GetRange(ctx, key, 0, desc.Size+1)
	if err != nil {
		return PartReceipt{}, storePartError(err, "read signed transfer part")
	}
	defer func() { _ = reader.Close() }()
	raw := newCountingHashWriter(io.Discard)
	if _, err := io.Copy(raw, reader); err != nil {
		return PartReceipt{}, storePartError(err, "verify signed transfer part")
	}
	if raw.N() > desc.Size {
		return PartReceipt{}, apperrors.New(apperrors.CodeQuotaExceeded, "signed part exceeds descriptor size")
	}
	digest := raw.SumHex()
	if err := verifyPartDigest(desc, raw.N(), digest); err != nil {
		return PartReceipt{}, err
	}
	object.Key = key
	if object.Size == 0 {
		object.Size = raw.N()
	}
	return newReceipt(plan, desc, object, raw.N(), raw.N(), digest, digest, EncodingIdentity, m.clock()), nil
}

func verifyPartDigest(desc PartDescriptor, rawSize int64, digest string) error {
	if rawSize != desc.Size {
		return apperrors.New(apperrors.CodeValidation, "part size does not match descriptor")
	}
	if !strings.EqualFold(digest, desc.ExpectedRawSHA256) {
		return apperrors.New(apperrors.CodeValidation, "part sha256 does not match descriptor")
	}
	return nil
}

func (m *Manager) buildManifest(plan TransferPlan, receipts []PartReceipt, expectedRoot string) (TransferManifest, error) {
	sort.Slice(receipts, func(i, j int) bool {
		return receipts[i].PartNumber < receipts[j].PartNumber
	})
	if err := verifyReceiptsCoverPlan(plan, receipts); err != nil {
		return TransferManifest{}, err
	}
	root := manifestRoot(receipts)
	if expectedRoot != "" && !strings.EqualFold(expectedRoot, root) {
		return TransferManifest{}, apperrors.New(apperrors.CodeValidation, "manifest root sha256 does not match")
	}
	return TransferManifest{
		TransferID:     plan.TransferID,
		OrganizationID: plan.OrganizationID,
		CorrelationID:  plan.CorrelationID,
		TotalSize:      plan.TotalSize,
		ChunkSize:      plan.ChunkSize,
		ContentType:    plan.ContentType,
		Compression:    plan.Compression,
		RootSHA256:     root,
		ManifestKey:    plan.ManifestKey,
		Parts:          append([]PartReceipt(nil), receipts...),
		CompletedAt:    m.clock(),
	}, nil
}

func verifyReceiptsCoverPlan(plan TransferPlan, receipts []PartReceipt) error {
	if plan.TotalSize == 0 && len(receipts) == 0 {
		return nil
	}
	var nextOffset int64
	for i, receipt := range receipts {
		if receipt.PartNumber != i {
			return apperrors.New(apperrors.CodePrecondition, "part numbers must be contiguous")
		}
		if receipt.Offset != nextOffset {
			return apperrors.New(apperrors.CodePrecondition, "part offsets must be contiguous")
		}
		var ok bool
		nextOffset, ok = checkedAddInt64(nextOffset, receipt.RawSize)
		if !ok {
			return apperrors.New(apperrors.CodePrecondition, "part offsets overflow transfer size")
		}
	}
	if nextOffset != plan.TotalSize {
		return apperrors.New(apperrors.CodePrecondition, "received parts do not cover total_size")
	}
	return nil
}

func (m *Manager) persistManifest(ctx context.Context, manifest TransferManifest) error {
	payload, err := json.Marshal(manifest)
	if err != nil {
		return apperrors.Wrap(err, apperrors.CodeInternal, "marshal transfer manifest")
	}
	_, err = m.objects.PutStream(ctx, manifest.ManifestKey, bytes.NewReader(payload), int64(len(payload)), objectstore.PutOptions{
		ContentType: "application/json",
		Metadata: map[string]string{
			"transfer_id":     manifest.TransferID,
			"organization_id": manifest.OrganizationID,
			"root_sha256":     manifest.RootSHA256,
		},
	})
	if err != nil {
		return apperrors.Wrap(err, apperrors.CodeDependency, "store transfer manifest")
	}
	if err := m.state.SaveManifest(ctx, manifest); err != nil {
		return apperrors.Wrap(err, apperrors.CodeDependency, "save transfer manifest")
	}
	return nil
}

func (m *Manager) openIdentityRange(ctx context.Context, manifest TransferManifest, offset, length int64) ([]io.ReadCloser, []int, error) {
	rangeEnd, ok := checkedAddInt64(offset, length)
	if offset < 0 || length <= 0 || !ok || rangeEnd > manifest.TotalSize {
		return nil, nil, apperrors.New(apperrors.CodeValidation, "range is outside transfer bounds")
	}
	remaining := length
	position := offset
	readers := make([]io.ReadCloser, 0, 2)
	partNumbers := make([]int, 0, 2)
	for _, part := range manifest.Parts {
		if remaining == 0 {
			break
		}
		if part.Encoding != EncodingIdentity {
			err := apperrors.New(apperrors.CodeNotImplemented, "range reads require identity-encoded chunks")
			return nil, nil, closeReadersWithError(readers, err)
		}
		reader, used, err := m.openPartRange(ctx, part, position, remaining)
		if err != nil {
			return nil, nil, closeReadersWithError(readers, err)
		}
		if reader != nil {
			readers = append(readers, reader)
			partNumbers = append(partNumbers, part.PartNumber)
			position += used
			remaining -= used
		}
	}
	if remaining != 0 {
		err := apperrors.New(apperrors.CodePrecondition, "range does not resolve against manifest")
		return nil, nil, closeReadersWithError(readers, err)
	}
	return readers, partNumbers, nil
}

func (m *Manager) forEachIdentityRange(ctx context.Context, manifest TransferManifest, offset, length int64, fn func(RangePart) error) (RangeDescriptor, error) {
	rangeEnd, ok := checkedAddInt64(offset, length)
	if offset < 0 || length <= 0 || !ok || rangeEnd > manifest.TotalSize {
		return RangeDescriptor{}, apperrors.New(apperrors.CodeValidation, "range is outside transfer bounds")
	}
	remaining := length
	position := offset
	desc := RangeDescriptor{
		TransferID: manifest.TransferID,
		Offset:     offset,
		Length:     length,
		Parts:      make([]int, 0, 2),
	}
	for _, part := range manifest.Parts {
		if remaining == 0 {
			break
		}
		if part.Encoding != EncodingIdentity {
			return RangeDescriptor{}, apperrors.New(apperrors.CodeNotImplemented, "range reads require identity-encoded chunks")
		}
		reader, used, err := m.openPartRange(ctx, part, position, remaining)
		if err != nil {
			return RangeDescriptor{}, err
		}
		if reader == nil {
			continue
		}
		rangePart := RangePart{
			TransferID: manifest.TransferID,
			PartNumber: part.PartNumber,
			Offset:     position,
			Length:     used,
			Reader:     reader,
		}
		callErr := fn(rangePart)
		closeErr := reader.Close()
		if callErr != nil {
			if closeErr != nil {
				return RangeDescriptor{}, apperrors.Wrap(closeErr, apperrors.CodeDependency, callErr.Error())
			}
			return RangeDescriptor{}, callErr
		}
		if closeErr != nil {
			return RangeDescriptor{}, apperrors.Wrap(closeErr, apperrors.CodeDependency, "close range reader")
		}
		desc.Parts = append(desc.Parts, part.PartNumber)
		position += used
		remaining -= used
	}
	if remaining != 0 {
		return RangeDescriptor{}, apperrors.New(apperrors.CodePrecondition, "range does not resolve against manifest")
	}
	return desc, nil
}

func (m *Manager) openPartRange(ctx context.Context, part PartReceipt, offset, length int64) (io.ReadCloser, int64, error) {
	partEnd, ok := checkedAddInt64(part.Offset, part.RawSize)
	if !ok {
		return nil, 0, apperrors.New(apperrors.CodePrecondition, "part range overflows transfer bounds")
	}
	rangeEnd, ok := checkedAddInt64(offset, length)
	if !ok {
		return nil, 0, apperrors.New(apperrors.CodeValidation, "range is outside transfer bounds")
	}
	if offset >= partEnd || rangeEnd <= part.Offset {
		return nil, 0, nil
	}
	start := max64(offset, part.Offset)
	end := min64(rangeEnd, partEnd)
	reader, _, err := m.objects.GetRange(ctx, part.ObjectKey, start-part.Offset, end-start)
	return reader, end - start, err
}

func (m *Manager) emit(ctx context.Context, eventType string, payload map[string]any) error {
	if m.events == nil {
		return nil
	}
	md := metadata.FromContext(ctx)
	md.EnsureCorrelation()
	env := events.Envelope{
		EventType:       eventType,
		Payload:         payload,
		PayloadEncoding: events.PayloadEncodingJSON,
		Metadata:        md.ToMap(),
		CorrelationID:   md.CorrelationID,
		SchemaVersion:   events.EnvelopeSchemaVersion,
		Timestamp:       m.clock(),
	}
	if err := m.events.Publish(ctx, env); err != nil {
		return apperrors.Wrap(err, apperrors.CodeDependency, "publish bulk event")
	}
	return nil
}

func (m *Manager) emitFailure(ctx context.Context, eventType, transferID string, err error) {
	if m.events == nil {
		return
	}
	_ = m.emit(ctx, eventType, failurePayload(transferID, err))
}

func (m *Manager) cacheReceipt(ctx context.Context, receipt PartReceipt) {
	if m.cache == nil {
		return
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return
	}
	_ = m.cache.Set(ctx, m.cacheKey(receipt.OrganizationID, receipt.TransferID, "part", fmt.Sprint(receipt.PartNumber)), payload, m.receiptTTL)
}

func (m *Manager) cacheProgress(ctx context.Context, plan TransferPlan, bytesAccepted int64, state string) {
	if m.cache == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"transfer_id":     plan.TransferID,
		"organization_id": plan.OrganizationID,
		"bytes_accepted":  bytesAccepted,
		"state":           state,
		"updated_at":      m.clock().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	_ = m.cache.Set(ctx, m.cacheKey(plan.OrganizationID, plan.TransferID, "progress"), payload, m.receiptTTL)
}

func (m *Manager) cacheKey(parts ...string) string {
	size := len(m.namespace)
	for _, part := range parts {
		size += 1 + len(part)
	}
	var builder strings.Builder
	builder.Grow(size)
	builder.WriteString(m.namespace)
	for _, part := range parts {
		builder.WriteByte(':')
		builder.WriteString(part)
	}
	return builder.String()
}

func newReceipt(plan TransferPlan, desc PartDescriptor, object objectstore.Object, rawSize, encodedSize int64, rawDigest, encodedDigest, encoding string, now time.Time) PartReceipt {
	return PartReceipt{
		TransferID:     plan.TransferID,
		OrganizationID: plan.OrganizationID,
		CorrelationID:  plan.CorrelationID,
		PartNumber:     desc.PartNumber,
		Offset:         desc.Offset,
		RawSize:        rawSize,
		EncodedSize:    encodedSize,
		RawSHA256:      rawDigest,
		EncodedSHA256:  encodedDigest,
		Encoding:       encoding,
		ObjectKey:      object.Key,
		ObjectETag:     object.ETag,
		CreatedAt:      now,
	}
}

func partPutOptions(plan TransferPlan, desc PartDescriptor, encoding string) objectstore.PutOptions {
	contentType := firstNonEmpty(desc.ContentType, plan.ContentType, "application/octet-stream")
	return objectstore.PutOptions{
		ContentType: contentType,
		Metadata: map[string]string{
			"transfer_id":     plan.TransferID,
			"organization_id": plan.OrganizationID,
			"part_number":     fmt.Sprint(desc.PartNumber),
			"encoding":        encoding,
		},
	}
}

func partObjectKey(plan TransferPlan, partNumber int) string {
	key := make([]byte, 0, len(plan.ObjectPrefix)+13)
	key = append(key, plan.ObjectPrefix...)
	key = append(key, "/parts/"...)
	if partNumber < 1_000_000 {
		for width := 100_000; width > 0; width /= 10 {
			key = append(key, byte('0'+partNumber/width%10))
		}
		return string(key)
	}
	key = strconv.AppendInt(key, int64(partNumber), 10)
	return string(key)
}

func manifestRoot(receipts []PartReceipt) string {
	h := sha256.New()
	for _, receipt := range receipts {
		var raw [sha256.Size]byte
		if _, err := hex.Decode(raw[:], []byte(receipt.RawSHA256)); err != nil {
			_, _ = h.Write([]byte(receipt.RawSHA256))
			continue
		}
		_, _ = h.Write(raw[:])
	}
	return sha256HashHex(h)
}

func storePartError(err error, message string) error {
	if _, ok := apperrors.As(err); ok {
		return err
	}
	return apperrors.Wrap(err, apperrors.CodeDependency, message)
}

func normalizeEncoding(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", EncodingIdentity:
		return EncodingIdentity
	case EncodingAuto:
		return EncodingAuto
	case EncodingBrotli, "brotli":
		return EncodingBrotli
	case EncodingZstd:
		return EncodingZstd
	case EncodingGzip:
		return EncodingGzip
	default:
		return ""
	}
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	var decoded [sha256.Size]byte
	_, err := hex.Decode(decoded[:], []byte(value))
	return err == nil
}

func newTransferID(now time.Time) string {
	var random [6]byte
	_, _ = rand.Read(random[:])
	return fmt.Sprintf("bt_%s_%s", now.UTC().Format("20060102T150405.000000000"), hex.EncodeToString(random[:]))
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func ceilDiv(n, d int64) int64 {
	if n == 0 {
		return 0
	}
	return 1 + (n-1)/d
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func checkedAddInt64(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if b > 0 && a > maxInt64-b {
		return 0, false
	}
	return a + b, true
}

func failurePayload(transferID string, err error) map[string]any {
	return map[string]any{
		"transfer_id": transferID,
		"error":       err.Error(),
	}
}

func planPayload(plan TransferPlan) map[string]any {
	return map[string]any{
		"transfer_id":     plan.TransferID,
		"organization_id": plan.OrganizationID,
		"total_size":      plan.TotalSize,
		"chunk_size":      plan.ChunkSize,
		"state":           plan.State,
	}
}

func partPayload(plan TransferPlan, desc PartDescriptor) map[string]any {
	return map[string]any{
		"transfer_id":     plan.TransferID,
		"organization_id": plan.OrganizationID,
		"part_number":     desc.PartNumber,
		"offset":          desc.Offset,
		"size":            desc.Size,
	}
}

func receiptPayload(receipt PartReceipt) map[string]any {
	return map[string]any{
		"transfer_id":     receipt.TransferID,
		"organization_id": receipt.OrganizationID,
		"part_number":     receipt.PartNumber,
		"raw_size":        receipt.RawSize,
		"raw_sha256":      receipt.RawSHA256,
		"encoding":        receipt.Encoding,
	}
}

func manifestPayload(manifest TransferManifest) map[string]any {
	return map[string]any{
		"transfer_id":     manifest.TransferID,
		"organization_id": manifest.OrganizationID,
		"total_size":      manifest.TotalSize,
		"root_sha256":     manifest.RootSHA256,
	}
}

func statusPayload(status TransferStatus) map[string]any {
	missing := make([]map[string]any, 0, len(status.MissingParts))
	for _, part := range status.MissingParts {
		missing = append(missing, map[string]any{
			"part_number": part.PartNumber,
			"offset":      part.Offset,
			"size":        part.Size,
		})
	}
	return map[string]any{
		"transfer_id":     status.TransferID,
		"organization_id": status.OrganizationID,
		"state":           status.State,
		"total_size":      status.TotalSize,
		"chunk_size":      status.ChunkSize,
		"bytes_accepted":  status.BytesAccepted,
		"parts_accepted":  status.PartsAccepted,
		"missing_parts":   missing,
		"manifest_key":    status.ManifestKey,
		"root_sha256":     status.RootSHA256,
		"resume_token":    status.ResumeToken,
		"updated_at":      status.UpdatedAt.Format(time.RFC3339Nano),
	}
}

func lanePayload(lane LaneDiagnostics) map[string]any {
	return map[string]any{
		"ingress":              lane.Ingress,
		"object_store_backend": lane.ObjectStoreBackend,
		"chunk_size":           lane.ChunkSize,
		"compression":          lane.Compression,
		"copy_budget":          lane.CopyBudget,
		"memory_budget":        lane.MemoryBudget,
		"resume_supported":     lane.ResumeSupported,
		"distributed_state":    lane.DistributedState,
		"zero_copy_available":  lane.ZeroCopyAvailable,
		"mptcp_available":      lane.MPTCPAvailable,
		"quic_available":       lane.QUICAvailable,
		"kernel_pacing":        lane.KernelPacing,
		"deadline_risk":        lane.DeadlineRisk,
		"fallback":             lane.Fallback,
		"attributes":           cloneStringMap(lane.Attributes),
	}
}

func signedPartGrantPayload(grant SignedPartGrant) map[string]any {
	return map[string]any{
		"transfer_id":  grant.TransferID,
		"part_number":  grant.PartNumber,
		"offset":       grant.Offset,
		"size":         grant.Size,
		"object_key":   grant.ObjectKey,
		"upload_url":   grant.UploadURL,
		"content_type": grant.ContentType,
		"headers":      cloneStringMap(grant.Headers),
		"expires_at":   grant.ExpiresAt.Format(time.RFC3339Nano),
	}
}

type hashingReader struct {
	reader io.Reader
	hash   hash.Hash
	n      int64
}

func newHashingReader(reader io.Reader) *hashingReader {
	return &hashingReader{reader: reader, hash: sha256.New()}
}

func (r *hashingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		_, _ = r.hash.Write(p[:n])
		r.n += int64(n)
	}
	return n, err
}

func (r *hashingReader) N() int64 {
	return r.n
}

func (r *hashingReader) SumHex() string {
	return sha256HashHex(r.hash)
}

type boundedReader struct {
	reader    io.Reader
	remaining int64
}

func newBoundedReader(reader io.Reader, limit int64) *boundedReader {
	return &boundedReader{reader: reader, remaining: limit}
}

func (r *boundedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		var probe [1]byte
		n, err := r.reader.Read(probe[:])
		if n > 0 {
			return 0, apperrors.New(apperrors.CodeQuotaExceeded, "part exceeds memory budget")
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

type countingHashWriter struct {
	writer io.Writer
	hash   hash.Hash
	n      int64
}

func newCountingHashWriter(writer io.Writer) *countingHashWriter {
	return &countingHashWriter{writer: writer, hash: sha256.New()}
}

func (w *countingHashWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		_, _ = w.hash.Write(p[:n])
		w.n += int64(n)
	}
	return n, err
}

func (w *countingHashWriter) N() int64 {
	return w.n
}

func (w *countingHashWriter) SumHex() string {
	return sha256HashHex(w.hash)
}

type gzipResult struct {
	rawSize   int64
	rawSHA256 string
	err       error
}

func compressedPartReader(reader io.Reader, limit int64, encoding string) (*io.PipeReader, *countingHashWriter, <-chan gzipResult, error) {
	pipeReader, pipeWriter := io.Pipe()
	raw := newHashingReader(newBoundedReader(reader, limit))
	encoded := newCountingHashWriter(pipeWriter)
	result := make(chan gzipResult, 1)
	writer, err := newCompressionWriter(encoded, encoding)
	if err != nil {
		_ = pipeReader.CloseWithError(err)
		_ = pipeWriter.CloseWithError(err)
		close(result)
		return nil, nil, result, err
	}
	go func() {
		defer close(result)
		_, copyErr := io.Copy(writer, raw)
		closeErr := writer.Close()
		if copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			_ = pipeWriter.CloseWithError(copyErr)
		} else {
			_ = pipeWriter.Close()
		}
		result <- gzipResult{rawSize: raw.N(), rawSHA256: raw.SumHex(), err: copyErr}
	}()
	return pipeReader, encoded, result, nil
}

func newCompressionWriter(writer io.Writer, encoding string) (io.WriteCloser, error) {
	switch encoding {
	case EncodingBrotli:
		return brotli.NewWriterLevel(writer, brotli.BestSpeed), nil
	case EncodingZstd:
		return zstd.NewWriter(writer, zstd.WithEncoderLevel(zstd.SpeedFastest))
	case EncodingGzip:
		return gzip.NewWriterLevel(writer, gzip.BestSpeed)
	default:
		return nil, apperrors.New(apperrors.CodeValidation, "unsupported compression")
	}
}

func readBoundedPart(reader io.Reader, limit int64) ([]byte, string, error) {
	maxInt := int64(int(^uint(0) >> 1))
	if limit < 0 || limit > maxInt {
		return nil, "", apperrors.New(apperrors.CodeValidation, "part size is outside platform bounds")
	}
	payload := make([]byte, int(limit))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, "", err
	}
	var probe [1]byte
	if n, err := reader.Read(probe[:]); n > 0 {
		return nil, "", apperrors.New(apperrors.CodeQuotaExceeded, "part exceeds memory budget")
	} else if err != nil && err != io.EOF {
		return nil, "", err
	}
	return payload, sha256Hex(payload), nil
}

func compressAuto(payload []byte) ([]byte, string, error) {
	if !likelyCompressible(payload) {
		return payload, EncodingIdentity, nil
	}
	compressed, err := compressBytes(payload, EncodingGzip)
	if err != nil {
		return nil, "", err
	}
	if len(compressed) >= len(payload) {
		return payload, EncodingIdentity, nil
	}
	return compressed, EncodingGzip, nil
}

func likelyCompressible(payload []byte) bool {
	if len(payload) < 128 {
		return false
	}
	const buckets = 256
	var seen [buckets]bool
	unique := 0
	repeats := 0
	step := max(1, len(payload)/1024)
	var prev byte
	hasPrev := false
	samples := 0
	for i := 0; i < len(payload); i += step {
		value := payload[i]
		if !seen[value] {
			seen[value] = true
			unique++
		}
		if hasPrev && value == prev {
			repeats++
		}
		prev = value
		hasPrev = true
		samples++
	}
	return unique <= samples/8 || repeats >= samples/16
}

func compressBytes(payload []byte, encoding string) ([]byte, error) {
	var buf bytes.Buffer
	writer, err := newCompressionWriter(&buf, encoding)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(payload); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return sha256BytesHex(sum)
}

func sha256HashHex(h hash.Hash) string {
	var sum [sha256.Size]byte
	return sha256BytesHex([sha256.Size]byte(h.Sum(sum[:0])))
}

func sha256BytesHex(sum [sha256.Size]byte) string {
	var encoded [sha256.Size * 2]byte
	hex.Encode(encoded[:], sum[:])
	return string(encoded[:])
}

type aggregateReadCloser struct {
	readers []io.ReadCloser
	index   int
}

func multiReadCloser(readers []io.ReadCloser) io.ReadCloser {
	switch len(readers) {
	case 0:
		return io.NopCloser(bytes.NewReader(nil))
	case 1:
		return readers[0]
	default:
		return &aggregateReadCloser{readers: readers}
	}
}

func (r *aggregateReadCloser) Read(p []byte) (int, error) {
	for r.index < len(r.readers) {
		n, err := r.readers[r.index].Read(p)
		if err == io.EOF && n == 0 {
			r.index++
			continue
		}
		return n, err
	}
	return 0, io.EOF
}

func (r *aggregateReadCloser) Close() error {
	return closeReaders(r.readers)
}

func closeReaders(readers []io.ReadCloser) error {
	var first error
	for _, reader := range readers {
		if err := reader.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func closeReadersWithError(readers []io.ReadCloser, err error) error {
	if closeErr := closeReaders(readers); closeErr != nil {
		return apperrors.Wrap(closeErr, apperrors.CodeDependency, err.Error())
	}
	return err
}
