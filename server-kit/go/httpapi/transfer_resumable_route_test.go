package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/bulk"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

const testChunkSize = 8

func newBulkManager(t *testing.T, bus bulk.EventBus) *bulk.Manager {
	t.Helper()
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://resumable-tests",
		Bucket:   "bulk",
	})
	m, err := bulk.NewManager(bulk.Options{
		ObjectStore:      store,
		StateStore:       bulk.NewMemoryStateStore(),
		EventBus:         bus,
		DefaultChunkSize: testChunkSize,
		MaxChunkSize:     1 << 20,
	})
	if err != nil {
		t.Fatalf("bulk.NewManager: %v", err)
	}
	return m
}

func resumableRoutes(t *testing.T, mgr TransferManager, blk ResumableByteStore) map[string]map[string]http.HandlerFunc {
	t.Helper()
	routes, err := MakeResumableTransferRoutes(ResumableTransferConfig{
		BasePath:      "/media/upload",
		EventType:     "media:upload:v1:requested",
		Manager:       mgr,
		Bulk:          blk,
		MaxChunkBytes: 1 << 20,
		NewTransferID: func() string { return "tx-resumable" },
	})
	if err != nil {
		t.Fatalf("MakeResumableTransferRoutes: %v", err)
	}
	byMethod := map[string]map[string]http.HandlerFunc{}
	for _, route := range routes {
		if byMethod[route.Method] == nil {
			byMethod[route.Method] = map[string]http.HandlerFunc{}
		}
		byMethod[route.Method][route.Path] = route.Handler
	}
	return byMethod
}

// orgRequest builds a request carrying an authenticated org and the transfer_id
// path value the chi-less std mux would otherwise populate.
func orgRequest(method, target string, body string, transferID string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	req = req.WithContext(security.ContextWithOrganizationID(req.Context(), "org-1"))
	if transferID != "" {
		req.SetPathValue("transfer_id", transferID)
	}
	return req
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func doCreate(t *testing.T, routes map[string]map[string]http.HandlerFunc, total int) CreateResumableResponse {
	t.Helper()
	req := orgRequest(http.MethodPost, "/media/upload", "", "")
	req.Header.Set(headerUploadLength, strconv.Itoa(total))
	rec := httptest.NewRecorder()
	routes[http.MethodPost]["/media/upload"](rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp CreateResumableResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return resp
}

func patchChunk(routes map[string]map[string]http.HandlerFunc, id string, offset int, data string) *httptest.ResponseRecorder {
	req := orgRequest(http.MethodPatch, "/media/upload/"+id, data, id)
	req.Header.Set(headerUploadOffset, strconv.Itoa(offset))
	req.Header.Set(headerChunkSHA256, sha256Hex(data))
	rec := httptest.NewRecorder()
	routes[http.MethodPatch]["/media/upload/{transfer_id}"](rec, req)
	return rec
}

func TestResumable_FullLifecycle(t *testing.T) {
	t.Parallel()
	bus := events.NewInMemoryBus(32)
	var mu sync.Mutex
	var bookends []string
	bus.Subscribe("media:upload:v1:*", func(_ context.Context, env events.Envelope) {
		mu.Lock()
		bookends = append(bookends, env.EventType)
		mu.Unlock()
	})
	mgr := newRouteManager(t, bus)
	blk := newBulkManager(t, nil)
	routes := resumableRoutes(t, mgr, blk)

	created := doCreate(t, routes, 16)
	if created.TransferID != "tx-resumable" || created.ChunkSize != testChunkSize {
		t.Fatalf("create response=%+v", created)
	}

	// First chunk: not complete -> 204 with new offset.
	rec := patchChunk(routes, created.TransferID, 0, "AAAAAAAA")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("chunk0 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(headerUploadOffset); got != "8" {
		t.Errorf("offset after chunk0=%q want 8", got)
	}

	// HEAD reports the resume offset.
	head := orgRequest(http.MethodHead, "/media/upload/"+created.TransferID, "", created.TransferID)
	hrec := httptest.NewRecorder()
	routes[http.MethodHead]["/media/upload/{transfer_id}"](hrec, head)
	if hrec.Code != http.StatusOK || hrec.Header().Get(headerUploadOffset) != "8" {
		t.Fatalf("HEAD status=%d offset=%q", hrec.Code, hrec.Header().Get(headerUploadOffset))
	}

	// Final chunk completes the upload -> 200 with manifest root.
	rec = patchChunk(routes, created.TransferID, 8, "BBBBBBBB")
	if rec.Code != http.StatusOK {
		t.Fatalf("final chunk status=%d body=%s", rec.Code, rec.Body.String())
	}
	var manifest ResumableManifestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.RootSHA256 == "" || manifest.TotalSize != 16 {
		t.Errorf("manifest=%+v", manifest)
	}
	if rec.Header().Get(headerUploadComplete) != "?1" {
		t.Errorf("Upload-Complete=%q want ?1", rec.Header().Get(headerUploadComplete))
	}

	// Bracketed by the durable bookends; tracker reaped on completion.
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(bookends, ",") != "media:upload:v1:requested,media:upload:v1:success" {
		t.Errorf("bookends=%v", bookends)
	}
	if mgr.Active() != 0 {
		t.Errorf("Active=%d want 0", mgr.Active())
	}
}

func TestResumable_IdempotentChunkReplay(t *testing.T) {
	t.Parallel()
	mgr := newRouteManager(t, nil)
	blk := newBulkManager(t, nil)
	routes := resumableRoutes(t, mgr, blk)
	created := doCreate(t, routes, 16)

	first := patchChunk(routes, created.TransferID, 0, "AAAAAAAA")
	if first.Code != http.StatusNoContent {
		t.Fatalf("chunk0 status=%d", first.Code)
	}
	// Re-send the same chunk: bulk replays the receipt, offset unchanged.
	replay := patchChunk(routes, created.TransferID, 0, "AAAAAAAA")
	if replay.Code != http.StatusNoContent {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	if got := replay.Header().Get(headerUploadOffset); got != "8" {
		t.Errorf("replay offset=%q want 8", got)
	}
}

func TestResumable_MisalignedOffsetRejected(t *testing.T) {
	t.Parallel()
	routes := resumableRoutes(t, newRouteManager(t, nil), newBulkManager(t, nil))
	created := doCreate(t, routes, 16)
	rec := patchChunk(routes, created.TransferID, 3, "AAAAAAAA") // 3 not a multiple of 8
	if rec.Code != http.StatusBadRequest {
		t.Errorf("misaligned status=%d want 400", rec.Code)
	}
}

func TestResumable_PatchValidation(t *testing.T) {
	t.Parallel()
	routes := resumableRoutes(t, newRouteManager(t, nil), newBulkManager(t, nil))
	created := doCreate(t, routes, 16)

	t.Run("missing checksum", func(t *testing.T) {
		req := orgRequest(http.MethodPatch, "/media/upload/"+created.TransferID, "AAAAAAAA", created.TransferID)
		req.Header.Set(headerUploadOffset, "0")
		rec := httptest.NewRecorder()
		routes[http.MethodPatch]["/media/upload/{transfer_id}"](rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status=%d want 400", rec.Code)
		}
	})

	t.Run("missing offset", func(t *testing.T) {
		req := orgRequest(http.MethodPatch, "/media/upload/"+created.TransferID, "AAAAAAAA", created.TransferID)
		req.Header.Set(headerChunkSHA256, sha256Hex("AAAAAAAA"))
		rec := httptest.NewRecorder()
		routes[http.MethodPatch]["/media/upload/{transfer_id}"](rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status=%d want 400", rec.Code)
		}
	})
}

func TestResumable_CreateRequiresUploadLength(t *testing.T) {
	t.Parallel()
	routes := resumableRoutes(t, newRouteManager(t, nil), newBulkManager(t, nil))
	req := orgRequest(http.MethodPost, "/media/upload", "", "")
	rec := httptest.NewRecorder()
	routes[http.MethodPost]["/media/upload"](rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", rec.Code)
	}
}

func TestResumable_StatusUnknownTransfer(t *testing.T) {
	t.Parallel()
	routes := resumableRoutes(t, newRouteManager(t, nil), newBulkManager(t, nil))
	req := orgRequest(http.MethodHead, "/media/upload/ghost", "", "ghost")
	rec := httptest.NewRecorder()
	routes[http.MethodHead]["/media/upload/{transfer_id}"](rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d want 404", rec.Code)
	}
}

func TestResumable_MissingOrgUnauthorized(t *testing.T) {
	t.Parallel()
	routes := resumableRoutes(t, newRouteManager(t, nil), newBulkManager(t, nil))
	// No org in context: bulk.Initiate fails closed.
	req := httptest.NewRequest(http.MethodPost, "/media/upload", nil)
	req.Header.Set(headerUploadLength, "16")
	rec := httptest.NewRecorder()
	routes[http.MethodPost]["/media/upload"](rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", rec.Code)
	}
}

func TestMakeResumableTransferRoutes_ConfigValidation(t *testing.T) {
	t.Parallel()
	mgr := newRouteManager(t, nil)
	blk := newBulkManager(t, nil)
	base := func() ResumableTransferConfig {
		return ResumableTransferConfig{
			BasePath: "/media/upload", EventType: "media:upload:v1:requested",
			Manager: mgr, Bulk: blk, MaxChunkBytes: 1024,
		}
	}
	cases := []struct {
		name   string
		mutate func(*ResumableTransferConfig)
	}{
		{"empty base path", func(c *ResumableTransferConfig) { c.BasePath = "" }},
		{"relative base path", func(c *ResumableTransferConfig) { c.BasePath = "media/upload" }},
		{"non-requested event", func(c *ResumableTransferConfig) { c.EventType = "media:upload:v1:success" }},
		{"nil manager", func(c *ResumableTransferConfig) { c.Manager = nil }},
		{"nil bulk", func(c *ResumableTransferConfig) { c.Bulk = nil }},
		{"non-positive chunk", func(c *ResumableTransferConfig) { c.MaxChunkBytes = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(&cfg)
			if _, err := MakeResumableTransferRoutes(cfg); err == nil {
				t.Errorf("%s: expected error", tc.name)
			}
		})
	}
}

// fakeBulk is a ResumableByteStore double for exercising failure paths that a
// real bulk.Manager would not deterministically produce.
type fakeBulk struct {
	chunkSize   int64
	total       int64
	accepted    int64
	acceptErr   error
	completeErr error
	statusErr   error
}

func (f *fakeBulk) Initiate(_ context.Context, req bulk.InitiateRequest) (bulk.TransferPlan, error) {
	return bulk.TransferPlan{
		TransferID:    req.TransferID,
		CorrelationID: "corr-fake",
		ChunkSize:     f.chunkSize,
		TotalSize:     req.TotalSize,
	}, nil
}

func (f *fakeBulk) Status(context.Context, string) (bulk.TransferStatus, error) {
	if f.statusErr != nil {
		return bulk.TransferStatus{}, f.statusErr
	}
	return bulk.TransferStatus{ChunkSize: f.chunkSize, TotalSize: f.total, BytesAccepted: f.accepted}, nil
}

func (f *fakeBulk) AcceptPart(_ context.Context, _ string, desc bulk.PartDescriptor, r io.Reader) (bulk.PartReceipt, error) {
	_, _ = io.Copy(io.Discard, r)
	if f.acceptErr != nil {
		return bulk.PartReceipt{}, f.acceptErr
	}
	f.accepted += desc.Size
	return bulk.PartReceipt{PartNumber: desc.PartNumber, RawSize: desc.Size}, nil
}

func (f *fakeBulk) Complete(context.Context, string, bulk.CompleteRequest) (bulk.TransferManifest, error) {
	if f.completeErr != nil {
		return bulk.TransferManifest{}, f.completeErr
	}
	return bulk.TransferManifest{RootSHA256: "root", TotalSize: f.total}, nil
}

func TestResumable_AcceptPartFailureFailsTransfer(t *testing.T) {
	t.Parallel()
	bus := events.NewInMemoryBus(16)
	var mu sync.Mutex
	var bookends []string
	bus.Subscribe("media:upload:v1:*", func(_ context.Context, env events.Envelope) {
		mu.Lock()
		bookends = append(bookends, env.EventType)
		mu.Unlock()
	})
	mgr := newRouteManager(t, bus)
	blk := &fakeBulk{chunkSize: 8, total: 16, acceptErr: errors.New("store exploded")}
	routes := resumableRoutes(t, mgr, blk)
	created := doCreate(t, routes, 16)

	rec := patchChunk(routes, created.TransferID, 0, "AAAAAAAA")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status=%d want 500", rec.Code)
	}
	if mgr.Active() != 0 {
		t.Errorf("Active=%d want 0 (failed transfer reaped)", mgr.Active())
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(bookends, ",") != "media:upload:v1:requested,media:upload:v1:failed" {
		t.Errorf("bookends=%v want requested,failed", bookends)
	}
}

func TestResumable_CompleteFailureFailsTransfer(t *testing.T) {
	t.Parallel()
	mgr := newRouteManager(t, nil)
	blk := &fakeBulk{chunkSize: 8, total: 8, completeErr: errors.New("manifest broke")}
	routes := resumableRoutes(t, mgr, blk)
	created := doCreate(t, routes, 8)

	rec := patchChunk(routes, created.TransferID, 0, "AAAAAAAA") // fills total -> Complete
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status=%d want 500", rec.Code)
	}
	if mgr.Active() != 0 {
		t.Errorf("Active=%d want 0", mgr.Active())
	}
}

func TestResumable_ChunkTooLarge(t *testing.T) {
	t.Parallel()
	mgr := newRouteManager(t, nil)
	blk := &fakeBulk{chunkSize: 8, total: 16}
	routes, err := MakeResumableTransferRoutes(ResumableTransferConfig{
		BasePath: "/media/upload", EventType: "media:upload:v1:requested",
		Manager: mgr, Bulk: blk, MaxChunkBytes: 4,
		NewTransferID: func() string { return "tx-big" },
	})
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	patch := routes[2].Handler
	req := orgRequest(http.MethodPatch, "/media/upload/tx-big", "AAAAAAAA", "tx-big")
	req.Header.Set(headerUploadOffset, "0")
	req.Header.Set(headerChunkSHA256, sha256Hex("AAAAAAAA"))
	rec := httptest.NewRecorder()
	patch(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400 (chunk too large)", rec.Code)
	}
}

func TestResumable_CreateBeginFailure(t *testing.T) {
	t.Parallel()
	blk := &fakeBulk{chunkSize: 8, total: 16}
	routes, err := MakeResumableTransferRoutes(ResumableTransferConfig{
		BasePath: "/media/upload", EventType: "media:upload:v1:requested",
		Manager: fakeManager{beginErr: errors.New("nope")}, Bulk: blk, MaxChunkBytes: 1024,
		NewTransferID: func() string { return "tx" },
	})
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	req := orgRequest(http.MethodPost, "/media/upload", "", "")
	req.Header.Set(headerUploadLength, "16")
	rec := httptest.NewRecorder()
	routes[0].Handler(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status=%d want 500", rec.Code)
	}
}
