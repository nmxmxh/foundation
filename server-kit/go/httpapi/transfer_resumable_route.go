package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bulk"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	apperrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/transfer"
)

// ResumableByteStore is the durable, offset-aware byte/part plane a resumable
// transfer needs. It is satisfied by *bulk.Manager: bulk already owns chunked
// multipart storage, idempotent part replay, manifests, and tenant/correlation
// scope, so the resumable route never reimplements any of that — it only maps
// HTTP semantics onto bulk and mirrors progress onto the transfer lane.
type ResumableByteStore interface {
	Initiate(ctx context.Context, req bulk.InitiateRequest) (bulk.TransferPlan, error)
	AcceptPart(ctx context.Context, transferID string, desc bulk.PartDescriptor, reader io.Reader) (bulk.PartReceipt, error)
	Status(ctx context.Context, transferID string) (bulk.TransferStatus, error)
	Complete(ctx context.Context, transferID string, req bulk.CompleteRequest) (bulk.TransferManifest, error)
}

// Resumable transfer HTTP headers (tus-inspired, kept minimal and explicit).
const (
	headerUploadLength   = "Upload-Length"
	headerUploadOffset   = "Upload-Offset"
	headerUploadComplete = "Upload-Complete"
	headerChunkSize      = "X-Chunk-Size"
	headerChunkSHA256    = "X-Chunk-SHA256"
)

// ResumableTransferConfig parameterizes MakeResumableTransferRoutes.
type ResumableTransferConfig struct {
	// BasePath is the create endpoint, e.g. "/media/upload". The status/patch
	// endpoints are mounted at BasePath + "/{transfer_id}". Required.
	BasePath string
	// EventType is the fact-lane bookend advertised for docs. Must be a
	// `:requested` event type matching the transfer Manager's identity. Required.
	EventType string
	// Description documents the routes.
	Description string
	// Manager owns the ephemeral progress lane and the durable bookends.
	// Required.
	Manager TransferManager
	// Bulk owns the durable byte/part plane. Required.
	Bulk ResumableByteStore
	// MaxChunkBytes hard-caps a single PATCH body (CP-02/CP-18). Required, > 0.
	MaxChunkBytes int64
	// NewTransferID mints a unique, ':'/whitespace-free transfer id. Defaults to
	// a correlation-style identifier.
	NewTransferID func() string
}

// CreateResumableResponse is returned by the create endpoint.
type CreateResumableResponse struct {
	TransferID    string `json:"transfer_id"`
	CorrelationID string `json:"correlation_id"`
	ChunkSize     int64  `json:"chunk_size"`
	Location      string `json:"location"`
}

// ResumableManifestResponse is returned when the final chunk settles the upload.
type ResumableManifestResponse struct {
	TransferID  string `json:"transfer_id"`
	RootSHA256  string `json:"root_sha256"`
	TotalSize   int64  `json:"total_size"`
	ManifestKey string `json:"manifest_key"`
}

// MakeResumableTransferRoutes returns the create (POST), status (HEAD), and
// chunk (PATCH) routes for a resumable upload. The byte plane is delegated to
// bulk; the transfer Manager mirrors progress and emits the durable bookends.
func MakeResumableTransferRoutes(cfg ResumableTransferConfig, opts ...RouteOption) ([]registry.HTTPRoute, error) {
	if err := validateResumableConfig(&cfg); err != nil {
		return nil, err
	}
	h := &resumableHandler{cfg: cfg, basePath: strings.TrimRight(cfg.BasePath, "/")}
	itemPath := h.basePath + "/{transfer_id}"

	build := func(method, path string, handler http.HandlerFunc) registry.HTTPRoute {
		route := registry.HTTPRoute{
			Method:      method,
			Path:        path,
			EventType:   cfg.EventType,
			Description: cfg.Description,
			IsStreaming: method == http.MethodPatch,
		}
		for _, opt := range opts {
			if opt != nil {
				opt(&route)
			}
		}
		if route.RequiredCapability == "" {
			route.RequiredCapability = security.CapabilityFromEvent(route.EventType)
		}
		if route.Permission == "" {
			route.Permission = security.PermissionFromEvent(route.EventType)
		}
		route.Handler = handler
		return route
	}

	return []registry.HTTPRoute{
		build(http.MethodPost, h.basePath, h.create),
		build(http.MethodHead, itemPath, h.status),
		build(http.MethodPatch, itemPath, h.patch),
	}, nil
}

func validateResumableConfig(cfg *ResumableTransferConfig) error {
	if strings.TrimSpace(cfg.BasePath) == "" {
		return fmt.Errorf("resumable transfer base path is required")
	}
	if !strings.HasPrefix(cfg.BasePath, "/") {
		return fmt.Errorf("resumable transfer base path %q must start with /", cfg.BasePath)
	}
	if !strings.HasSuffix(strings.TrimSpace(cfg.EventType), ":requested") {
		return fmt.Errorf("resumable transfer event_type %q must end in :requested", cfg.EventType)
	}
	if cfg.Manager == nil {
		return fmt.Errorf("resumable transfer manager is required")
	}
	if cfg.Bulk == nil {
		return fmt.Errorf("resumable transfer bulk store is required")
	}
	if cfg.MaxChunkBytes <= 0 {
		return fmt.Errorf("resumable transfer max chunk bytes must be positive")
	}
	if cfg.NewTransferID == nil {
		cfg.NewTransferID = metadata.NewCorrelationID
	}
	return nil
}

type resumableHandler struct {
	cfg      ResumableTransferConfig
	basePath string
}

// create initiates a durable bulk transfer and begins the progress lane.
func (h *resumableHandler) create(w http.ResponseWriter, r *http.Request) {
	ctx := ContextWithRequestMetadata(r)
	total, err := parseNonNegativeHeader(r, headerUploadLength)
	if err != nil {
		writeResumableValidation(w, "invalid_upload_length", "Upload-Length header must be a non-negative integer")
		return
	}
	transferID := strings.TrimSpace(h.cfg.NewTransferID())
	plan, err := h.cfg.Bulk.Initiate(ctx, bulk.InitiateRequest{
		TransferID:     transferID,
		TotalSize:      total,
		ContentType:    strings.TrimSpace(r.Header.Get("Content-Type")),
		IdempotencyKey: transferID,
	})
	if err != nil {
		apperrors.HTTPError(w, err)
		return
	}

	begin := transfer.BeginInput{TransferID: transferID, CorrelationID: plan.CorrelationID}
	if total > 0 {
		begin.BytesTotal = total
	}
	if _, err := h.cfg.Manager.Begin(ctx, begin); err != nil {
		domainerr.WriteHTTP(w, domainerr.Internal("transfer_begin_failed", "could not begin transfer"),
			domainerr.ResponseOptions{Status: http.StatusInternalServerError})
		return
	}

	location := h.basePath + "/" + transferID
	w.Header().Set("Location", location)
	w.Header().Set(headerChunkSize, strconv.FormatInt(plan.ChunkSize, 10))
	WriteJSON(w, http.StatusCreated, CreateResumableResponse{
		TransferID:    transferID,
		CorrelationID: plan.CorrelationID,
		ChunkSize:     plan.ChunkSize,
		Location:      location,
	})
}

// status answers a HEAD probe with the current resume offset so a client can
// continue from exactly where it left off.
func (h *resumableHandler) status(w http.ResponseWriter, r *http.Request) {
	ctx := ContextWithRequestMetadata(r)
	transferID := strings.TrimSpace(r.PathValue("transfer_id"))
	st, err := h.cfg.Bulk.Status(ctx, transferID)
	if err != nil {
		apperrors.HTTPError(w, err)
		return
	}
	complete := "?0"
	if st.TotalSize > 0 && st.BytesAccepted >= st.TotalSize {
		complete = "?1"
	}
	w.Header().Set(headerUploadOffset, strconv.FormatInt(st.BytesAccepted, 10))
	w.Header().Set(headerUploadLength, strconv.FormatInt(st.TotalSize, 10))
	w.Header().Set(headerChunkSize, strconv.FormatInt(st.ChunkSize, 10))
	w.Header().Set(headerUploadComplete, complete)
	w.WriteHeader(http.StatusOK)
}

// patch accepts one chunk at a declared offset, mirrors progress, and finalizes
// the transfer once the byte plane reports full coverage.
func (h *resumableHandler) patch(w http.ResponseWriter, r *http.Request) {
	ctx := ContextWithRequestMetadata(r)
	transferID := strings.TrimSpace(r.PathValue("transfer_id"))
	chunk, herr := h.parseChunk(r)
	if herr != nil {
		writeResumableValidation(w, herr.code, herr.message)
		return
	}

	st, err := h.cfg.Bulk.Status(ctx, transferID)
	if err != nil {
		apperrors.HTTPError(w, err)
		return
	}
	if chunk.offset%st.ChunkSize != 0 {
		writeResumableValidation(w, "misaligned_offset", "Upload-Offset must be a multiple of the chunk size")
		return
	}

	desc := bulk.PartDescriptor{
		PartNumber:        int(chunk.offset / st.ChunkSize),
		Offset:            chunk.offset,
		Size:              chunk.size,
		ExpectedRawSHA256: chunk.sha256,
		ContentType:       strings.TrimSpace(r.Header.Get("Content-Type")),
	}
	body := http.MaxBytesReader(w, r.Body, h.cfg.MaxChunkBytes)
	if _, err := h.cfg.Bulk.AcceptPart(ctx, transferID, desc, body); err != nil {
		h.failProgress(ctx, transferID, err)
		apperrors.HTTPError(w, err)
		return
	}

	after, err := h.cfg.Bulk.Status(ctx, transferID)
	if err != nil {
		apperrors.HTTPError(w, err)
		return
	}
	h.advanceProgress(transferID, after.BytesAccepted)
	h.respondAfterChunk(ctx, w, transferID, after)
}

// respondAfterChunk completes the transfer when fully covered, else reports the
// new resume offset.
func (h *resumableHandler) respondAfterChunk(ctx context.Context, w http.ResponseWriter, transferID string, st bulk.TransferStatus) {
	if st.TotalSize <= 0 || st.BytesAccepted < st.TotalSize {
		w.Header().Set(headerUploadOffset, strconv.FormatInt(st.BytesAccepted, 10))
		w.Header().Set(headerUploadComplete, "?0")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	manifest, err := h.cfg.Bulk.Complete(ctx, transferID, bulk.CompleteRequest{})
	if err != nil {
		h.failProgress(ctx, transferID, err)
		apperrors.HTTPError(w, err)
		return
	}
	if err := h.cfg.Manager.Complete(ctx, transferID, manifest.RootSHA256); err != nil {
		domainerr.WriteHTTP(w, domainerr.Internal("transfer_complete_failed", "could not finalize transfer"),
			domainerr.ResponseOptions{Status: http.StatusInternalServerError})
		return
	}
	w.Header().Set(headerUploadComplete, "?1")
	WriteJSON(w, http.StatusOK, ResumableManifestResponse{
		TransferID:  transferID,
		RootSHA256:  manifest.RootSHA256,
		TotalSize:   manifest.TotalSize,
		ManifestKey: manifest.ManifestKey,
	})
}

// advanceProgress mirrors the durable byte offset onto the ephemeral progress
// lane. It is best-effort: if the tracker is absent (e.g. after a restart) bulk
// remains the source of truth and the upload still proceeds.
func (h *resumableHandler) advanceProgress(transferID string, bytesAccepted int64) {
	mgr, ok := h.cfg.Manager.(interface {
		Get(string) (*transfer.Tracker, bool)
	})
	if !ok {
		return
	}
	if tracker, found := mgr.Get(transferID); found {
		_, _ = tracker.Advance(bytesAccepted)
	}
}

func (h *resumableHandler) failProgress(ctx context.Context, transferID string, cause error) {
	_ = h.cfg.Manager.Fail(ctx, transferID, cause.Error())
}

type chunkInput struct {
	offset int64
	size   int64
	sha256 string
}

type resumableErr struct{ code, message string }

func (h *resumableHandler) parseChunk(r *http.Request) (chunkInput, *resumableErr) {
	offset, err := parseNonNegativeHeader(r, headerUploadOffset)
	if err != nil {
		return chunkInput{}, &resumableErr{"invalid_upload_offset", "Upload-Offset header must be a non-negative integer"}
	}
	if r.ContentLength < 0 {
		return chunkInput{}, &resumableErr{"length_required", "Content-Length is required for a chunk"}
	}
	if r.ContentLength > h.cfg.MaxChunkBytes {
		return chunkInput{}, &resumableErr{"chunk_too_large", "chunk exceeds maximum size"}
	}
	sha := strings.ToLower(strings.TrimSpace(r.Header.Get(headerChunkSHA256)))
	if sha == "" {
		return chunkInput{}, &resumableErr{"missing_chunk_checksum", "X-Chunk-SHA256 header is required"}
	}
	return chunkInput{offset: offset, size: r.ContentLength, sha256: sha}, nil
}

func parseNonNegativeHeader(r *http.Request, name string) (int64, error) {
	raw := strings.TrimSpace(r.Header.Get(name))
	if raw == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return value, nil
}

func writeResumableValidation(w http.ResponseWriter, code, message string) {
	domainerr.WriteHTTP(w, domainerr.Validation(code, message), domainerr.ResponseOptions{Status: http.StatusBadRequest})
}
