package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/transfer"
)

// TransferStore is the byte-plane dependency a transfer route needs. It is
// satisfied by *objectstore.Store and kept narrow so handlers can be tested
// without a live S3/MinIO backend.
type TransferStore interface {
	PutStream(ctx context.Context, key string, reader io.Reader, size int64, opts objectstore.PutOptions) (objectstore.Object, error)
}

// TransferManager is the lifecycle-plane dependency. It is satisfied by
// *transfer.Manager. The route only ever begins, completes, or fails a
// transfer; progress fan-out is owned by the returned Tracker.
type TransferManager interface {
	Begin(ctx context.Context, in transfer.BeginInput) (*transfer.Tracker, error)
	Complete(ctx context.Context, id, checksum string) error
	Fail(ctx context.Context, id, reason string) error
}

// TransferRouteConfig parameterizes MakeTransferRoute.
type TransferRouteConfig struct {
	// Method is the HTTP verb. Defaults to PUT (PUT/POST are typical for upload).
	Method string
	// Path is the route pattern, e.g. "/media/upload". Required.
	Path string
	// EventType is the fact-lane bookend advertised for docs/catalog. It must be
	// a `:requested` event type and should match the Manager's identity. Required.
	EventType string
	// Description documents the route.
	Description string
	// Manager brackets the transfer with bookend events and owns its lifecycle.
	// Required.
	Manager TransferManager
	// Store receives the streamed bytes. Required.
	Store TransferStore
	// KeyPrefix namespaces stored object keys, e.g. "uploads". Required.
	KeyPrefix string
	// MaxBytes hard-caps the request body (CP-02 bounded, CP-18 ingress guard).
	// Required and must be positive.
	MaxBytes int64
	// NewTransferID mints a unique, ':'/whitespace-free transfer id. Defaults to
	// a correlation-style identifier.
	NewTransferID func() string
}

// TransferResponse is the success body returned once bytes have landed and the
// transfer has settled.
type TransferResponse struct {
	TransferID    string `json:"transfer_id"`
	CorrelationID string `json:"correlation_id"`
	Bucket        string `json:"bucket,omitempty"`
	Key           string `json:"key"`
	Size          int64  `json:"size"`
	ContentType   string `json:"content_type,omitempty"`
	ETag          string `json:"etag,omitempty"`
	URL           string `json:"url,omitempty"`
}

// progressReader wraps an upload body and reports the cumulative byte offset to
// a transfer.Tracker as data is consumed. It throttles updates so a small read
// size does not turn the progress lane into a hot loop; the tracker coalesces
// regardless, but throttling avoids needless lock traffic.
type progressReader struct {
	src       io.Reader
	tracker   *transfer.Tracker
	read      int64
	lastSent  int64
	threshold int64
}

func newProgressReader(src io.Reader, tracker *transfer.Tracker, threshold int64) *progressReader {
	if threshold <= 0 {
		threshold = 256 << 10 // 256 KiB
	}
	return &progressReader{src: src, tracker: tracker, threshold: threshold}
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.src.Read(b)
	if n > 0 {
		p.read += int64(n)
		if p.read-p.lastSent >= p.threshold {
			p.lastSent = p.read
			_, _ = p.tracker.Advance(p.read)
		}
	}
	if err == io.EOF && p.read != p.lastSent {
		p.lastSent = p.read
		_, _ = p.tracker.Advance(p.read)
	}
	return n, err
}

// MakeTransferRoute builds a streaming upload route that composes the byte plane
// (objectstore.PutStream) with the lifecycle plane (transfer.Manager). Unlike
// MakeEventRoute, the handler never buffers the whole body: bytes flow from the
// request straight into the store while progress is reported on the transfer's
// progress lane, and durable `:requested`/`:success`/`:failed` bookends bracket
// the operation on the fact lane.
func MakeTransferRoute(cfg TransferRouteConfig, opts ...RouteOption) (registry.HTTPRoute, error) {
	if err := validateTransferConfig(&cfg); err != nil {
		return registry.HTTPRoute{}, err
	}

	route := registry.HTTPRoute{
		Method:        cfg.Method,
		Path:          strings.TrimSpace(cfg.Path),
		EventType:     strings.TrimSpace(cfg.EventType),
		Description:   strings.TrimSpace(cfg.Description),
		StaticPayload: nil,
		IsStreaming:   true,
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
	route.Handler = transferHandler(cfg, route.Method)
	return route, nil
}

func validateTransferConfig(cfg *TransferRouteConfig) error {
	cfg.Method = strings.ToUpper(strings.TrimSpace(cfg.Method))
	if cfg.Method == "" {
		cfg.Method = http.MethodPut
	}
	if cfg.Method != http.MethodPut && cfg.Method != http.MethodPost {
		return fmt.Errorf("transfer route method %q must be PUT or POST", cfg.Method)
	}
	if strings.TrimSpace(cfg.Path) == "" {
		return fmt.Errorf("transfer route path is required")
	}
	et := strings.TrimSpace(cfg.EventType)
	if !strings.HasSuffix(et, ":requested") {
		return fmt.Errorf("transfer route event_type %q must end in :requested", et)
	}
	if cfg.Manager == nil {
		return fmt.Errorf("transfer route manager is required")
	}
	if cfg.Store == nil {
		return fmt.Errorf("transfer route store is required")
	}
	if strings.TrimSpace(cfg.KeyPrefix) == "" {
		return fmt.Errorf("transfer route key prefix is required")
	}
	if cfg.MaxBytes <= 0 {
		return fmt.Errorf("transfer route max bytes must be positive")
	}
	if cfg.NewTransferID == nil {
		cfg.NewTransferID = metadata.NewCorrelationID
	}
	return nil
}

func transferHandler(cfg TransferRouteConfig, method string) http.HandlerFunc {
	keyPrefix := strings.Trim(strings.TrimSpace(cfg.KeyPrefix), "/")
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(method, r.Method) {
			domainerr.WriteHTTP(w, domainerr.Validation("method_not_allowed", "method not allowed"),
				domainerr.ResponseOptions{Status: http.StatusMethodNotAllowed})
			return
		}
		if r.ContentLength > cfg.MaxBytes {
			domainerr.WriteHTTP(w, domainerr.Validation("payload_too_large", "upload exceeds maximum size"),
				domainerr.ResponseOptions{Status: http.StatusRequestEntityTooLarge})
			return
		}

		ctx := ContextWithRequestMetadata(r)
		md := metadata.FromContext(ctx)
		correlationID := md.EnsureCorrelation()
		transferID := strings.TrimSpace(cfg.NewTransferID())
		orgID := security.GetOrganizationIDFromContext(ctx)
		key := buildObjectKey(keyPrefix, orgID, transferID)

		size := r.ContentLength // -1 when unknown (chunked).
		begin := transfer.BeginInput{TransferID: transferID, CorrelationID: correlationID}
		if size > 0 {
			begin.BytesTotal = size
		}
		tracker, err := cfg.Manager.Begin(ctx, begin)
		if err != nil {
			domainerr.WriteHTTP(w, domainerr.Internal("transfer_begin_failed", "could not begin transfer"),
				domainerr.ResponseOptions{Status: http.StatusInternalServerError})
			return
		}

		obj, err := streamToStore(ctx, cfg, r, w, tracker, key, size)
		if err != nil {
			_ = cfg.Manager.Fail(ctx, transferID, err.Error())
			writeStreamError(w, err)
			return
		}

		if err := cfg.Manager.Complete(ctx, transferID, obj.ETag); err != nil {
			domainerr.WriteHTTP(w, domainerr.Internal("transfer_complete_failed", "could not finalize transfer"),
				domainerr.ResponseOptions{Status: http.StatusInternalServerError})
			return
		}

		WriteJSON(w, http.StatusOK, TransferResponse{
			TransferID:    transferID,
			CorrelationID: correlationID,
			Bucket:        obj.Bucket,
			Key:           obj.Key,
			Size:          obj.Size,
			ContentType:   obj.ContentType,
			ETag:          obj.ETag,
			URL:           obj.URL,
		})
	}
}

// streamToStore caps the body, wraps it with progress reporting, and streams it
// into the object store. The MaxBytesReader enforces the ingress ceiling even
// when ContentLength lies or is absent.
func streamToStore(ctx context.Context, cfg TransferRouteConfig, r *http.Request, w http.ResponseWriter, tracker *transfer.Tracker, key string, size int64) (objectstore.Object, error) {
	var body io.Reader = http.MaxBytesReader(w, r.Body, cfg.MaxBytes)
	body = newProgressReader(body, tracker, 0)

	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	obj, err := cfg.Store.PutStream(ctx, key, body, size, objectstore.PutOptions{
		ContentType: contentType,
		Metadata: map[string]string{
			"transfer_id":    tracker.ID(),
			"correlation_id": tracker.CorrelationID(),
		},
	})
	if err != nil {
		return objectstore.Object{}, err
	}
	return obj, nil
}

// writeStreamError maps a streaming failure to an HTTP status. A body that
// overruns MaxBytes surfaces as 413; everything else is a 400 (the client's
// stream broke) to avoid leaking internal detail.
func writeStreamError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		domainerr.WriteHTTP(w, domainerr.Validation("payload_too_large", "upload exceeds maximum size"),
			domainerr.ResponseOptions{Status: http.StatusRequestEntityTooLarge})
		return
	}
	domainerr.WriteHTTP(w, domainerr.Validation("transfer_stream_failed", "upload stream failed"),
		domainerr.ResponseOptions{Status: http.StatusBadRequest})
}

func buildObjectKey(prefix, orgID, transferID string) string {
	parts := make([]string, 0, 3)
	if prefix != "" {
		parts = append(parts, prefix)
	}
	if orgID = strings.TrimSpace(orgID); orgID != "" {
		parts = append(parts, orgID)
	}
	parts = append(parts, transferID)
	return strings.Join(parts, "/")
}
