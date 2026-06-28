package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/transfer"
)

func memoryStore(t *testing.T) *objectstore.Store {
	t.Helper()
	return objectstore.New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://transfer-route-tests",
		Bucket:   "test-bucket",
	})
}

func newRouteManager(t *testing.T, bus events.Bus) *transfer.Manager {
	t.Helper()
	m, err := transfer.NewManager(transfer.Config{
		Domain: "media", Action: "upload", Version: "v1", Bus: bus,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// busRecorder subscribes to the fact lane and records bookend event types.
func busRecorder() (*events.InMemoryBus, func() []string) {
	bus := events.NewInMemoryBus(16)
	var mu sync.Mutex
	var seen []string
	bus.Subscribe("media:upload:v1:*", func(_ context.Context, env events.Envelope) {
		mu.Lock()
		seen = append(seen, env.EventType)
		mu.Unlock()
	})
	return bus, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(seen))
		copy(out, seen)
		return out
	}
}

func baseConfig(m TransferManager, s TransferStore) TransferRouteConfig {
	return TransferRouteConfig{
		Method:        http.MethodPut,
		Path:          "/media/upload",
		EventType:     "media:upload:v1:requested",
		Manager:       m,
		Store:         s,
		KeyPrefix:     "uploads",
		MaxBytes:      1 << 20,
		NewTransferID: func() string { return "tx-fixed" },
	}
}

func TestMakeTransferRoute_HappyPath(t *testing.T) {
	t.Parallel()
	bus, seen := busRecorder()
	mgr := newRouteManager(t, bus)
	store := memoryStore(t)
	route, err := MakeTransferRoute(baseConfig(mgr, store))
	if err != nil {
		t.Fatalf("MakeTransferRoute: %v", err)
	}
	if !route.IsStreaming {
		t.Error("transfer route must be marked streaming")
	}

	body := "hello-streamed-bytes"
	req := httptest.NewRequest(http.MethodPut, "/media/upload", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	req = req.WithContext(security.ContextWithOrganizationID(req.Context(), "org-9"))
	rec := httptest.NewRecorder()

	route.Handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeTransferResponse(t, rec)
	if resp.TransferID != "tx-fixed" {
		t.Errorf("transfer_id=%q", resp.TransferID)
	}
	if resp.Size != int64(len(body)) {
		t.Errorf("size=%d want %d", resp.Size, len(body))
	}
	if resp.Key != "uploads/org-9/tx-fixed" {
		t.Errorf("key=%q want uploads/org-9/tx-fixed", resp.Key)
	}
	// Bytes actually landed in the store.
	got, err := store.ReadBytes(context.Background(), resp.Key)
	if err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	if string(got) != body {
		t.Errorf("stored=%q want %q", got, body)
	}
	// Fact-lane bookends bracket the transfer, and the tracker is reaped.
	if types := seen(); fmt.Sprint(types) != fmt.Sprint([]string{"media:upload:v1:requested", "media:upload:v1:success"}) {
		t.Errorf("bookends=%v", types)
	}
	if mgr.Active() != 0 {
		t.Errorf("Active=%d want 0", mgr.Active())
	}
}

func TestMakeTransferRoute_KeyWithoutOrg(t *testing.T) {
	t.Parallel()
	mgr := newRouteManager(t, nil)
	route, _ := MakeTransferRoute(baseConfig(mgr, memoryStore(t)))
	req := httptest.NewRequest(http.MethodPut, "/media/upload", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	route.Handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if resp := decodeTransferResponse(t, rec); resp.Key != "uploads/tx-fixed" {
		t.Errorf("key=%q want uploads/tx-fixed (no org segment)", resp.Key)
	}
}

func TestMakeTransferRoute_MethodNotAllowed(t *testing.T) {
	t.Parallel()
	mgr := newRouteManager(t, nil)
	route, _ := MakeTransferRoute(baseConfig(mgr, memoryStore(t)))
	req := httptest.NewRequest(http.MethodGet, "/media/upload", nil)
	rec := httptest.NewRecorder()
	route.Handler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d want 405", rec.Code)
	}
}

func TestMakeTransferRoute_ContentLengthCeilingRejectedBeforeBegin(t *testing.T) {
	t.Parallel()
	bus, seen := busRecorder()
	mgr := newRouteManager(t, bus)
	cfg := baseConfig(mgr, memoryStore(t))
	cfg.MaxBytes = 4
	route, _ := MakeTransferRoute(cfg)

	req := httptest.NewRequest(http.MethodPut, "/media/upload", strings.NewReader("way-too-long"))
	rec := httptest.NewRecorder()
	route.Handler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status=%d want 413", rec.Code)
	}
	// Rejected before any tracker/bookend was created.
	if types := seen(); len(types) != 0 {
		t.Errorf("no bookends expected, got %v", types)
	}
	if mgr.Active() != 0 {
		t.Errorf("Active=%d want 0", mgr.Active())
	}
}

func TestMakeTransferRoute_StreamOverrunFailsTransfer(t *testing.T) {
	t.Parallel()
	bus, seen := busRecorder()
	mgr := newRouteManager(t, bus)
	store := memoryStore(t)
	cfg := baseConfig(mgr, store)
	cfg.MaxBytes = 4
	route, _ := MakeTransferRoute(cfg)

	// Hide the true size (chunked-style) so the early ceiling check passes and
	// the MaxBytesReader trips mid-stream instead.
	req := httptest.NewRequest(http.MethodPut, "/media/upload", strings.NewReader("aaaaaaaaaa"))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	route.Handler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status=%d want 413", rec.Code)
	}
	// requested + failed: the transfer was begun then failed, never succeeded.
	if types := seen(); fmt.Sprint(types) != fmt.Sprint([]string{"media:upload:v1:requested", "media:upload:v1:failed"}) {
		t.Errorf("bookends=%v want requested,failed", types)
	}
	if mgr.Active() != 0 {
		t.Errorf("Active=%d want 0 (failed transfer reaped)", mgr.Active())
	}
	if _, err := store.ReadBytes(context.Background(), "uploads/tx-fixed"); err == nil {
		t.Error("partial object must not be committed on overrun")
	}
}

func TestMakeTransferRoute_StoreFailureFailsTransfer(t *testing.T) {
	t.Parallel()
	bus, seen := busRecorder()
	mgr := newRouteManager(t, bus)
	cfg := baseConfig(mgr, failingStore{err: errors.New("disk on fire")})
	route, _ := MakeTransferRoute(cfg)

	req := httptest.NewRequest(http.MethodPut, "/media/upload", strings.NewReader("data"))
	rec := httptest.NewRecorder()
	route.Handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d want 400", rec.Code)
	}
	if types := seen(); fmt.Sprint(types) != fmt.Sprint([]string{"media:upload:v1:requested", "media:upload:v1:failed"}) {
		t.Errorf("bookends=%v want requested,failed", types)
	}
}

func TestMakeTransferRoute_BeginFailureReturns500(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(fakeManager{beginErr: errors.New("nope")}, memoryStore(t))
	route, _ := MakeTransferRoute(cfg)
	req := httptest.NewRequest(http.MethodPut, "/media/upload", strings.NewReader("data"))
	rec := httptest.NewRecorder()
	route.Handler(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status=%d want 500", rec.Code)
	}
}

func TestMakeTransferRoute_CompleteFailureReturns500(t *testing.T) {
	t.Parallel()
	// Begin succeeds (real tracker) but Complete fails: bytes landed yet the
	// caller must learn finalization failed.
	tr, _ := transfer.NewTracker("tx-fixed", "corr", 0)
	cfg := baseConfig(fakeManager{tracker: tr, completeErr: errors.New("bus down")}, memoryStore(t))
	route, _ := MakeTransferRoute(cfg)
	req := httptest.NewRequest(http.MethodPut, "/media/upload", strings.NewReader("data"))
	rec := httptest.NewRecorder()
	route.Handler(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status=%d want 500", rec.Code)
	}
}

func TestMakeTransferRoute_ConfigValidation(t *testing.T) {
	t.Parallel()
	mgr := newRouteManager(t, nil)
	store := memoryStore(t)
	cases := []struct {
		name   string
		mutate func(*TransferRouteConfig)
	}{
		{"bad method", func(c *TransferRouteConfig) { c.Method = "DELETE" }},
		{"missing path", func(c *TransferRouteConfig) { c.Path = "" }},
		{"non-requested event", func(c *TransferRouteConfig) { c.EventType = "media:upload:v1:success" }},
		{"nil manager", func(c *TransferRouteConfig) { c.Manager = nil }},
		{"nil store", func(c *TransferRouteConfig) { c.Store = nil }},
		{"empty prefix", func(c *TransferRouteConfig) { c.KeyPrefix = "" }},
		{"non-positive max", func(c *TransferRouteConfig) { c.MaxBytes = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := baseConfig(mgr, store)
			tc.mutate(&cfg)
			if _, err := MakeTransferRoute(cfg); err == nil {
				t.Errorf("%s: expected error", tc.name)
			}
		})
	}
}

func TestMakeTransferRoute_OptionsOverrideCapabilityAndPermission(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(newRouteManager(t, nil), memoryStore(t))
	route, err := MakeTransferRoute(cfg,
		WithRequiredCapability("media.admin"),
		WithPermission("media:write"),
		WithTags("upload"),
	)
	if err != nil {
		t.Fatalf("MakeTransferRoute: %v", err)
	}
	if route.RequiredCapability != "media.admin" {
		t.Errorf("capability=%q want media.admin (option must win)", route.RequiredCapability)
	}
	if route.Permission != security.NormalizePermission("media:write") {
		t.Errorf("permission=%q want normalized media:write", route.Permission)
	}
}

func TestMakeTransferRoute_DefaultsMethodAndID(t *testing.T) {
	t.Parallel()
	cfg := baseConfig(newRouteManager(t, nil), memoryStore(t))
	cfg.Method = ""
	cfg.NewTransferID = nil
	route, err := MakeTransferRoute(cfg)
	if err != nil {
		t.Fatalf("MakeTransferRoute: %v", err)
	}
	if route.Method != http.MethodPut {
		t.Errorf("default method=%q want PUT", route.Method)
	}
	req := httptest.NewRequest(http.MethodPut, "/media/upload", strings.NewReader("x"))
	rec := httptest.NewRecorder()
	route.Handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if id := decodeTransferResponse(t, rec).TransferID; strings.TrimSpace(id) == "" {
		t.Error("default NewTransferID must mint a non-empty id")
	}
}

func TestProgressReader_ReportsThresholdAndEOF(t *testing.T) {
	t.Parallel()
	tr, _ := transfer.NewTracker("tx", "corr", 0)
	var mu sync.Mutex
	var offsets []int64
	tr.Subscribe(func(u transfer.Update) {
		mu.Lock()
		offsets = append(offsets, u.BytesDone)
		mu.Unlock()
	})

	// 1000 bytes delivered one byte per Read, threshold 400: reports at >=400,
	// >=800, then EOF flush at 1000.
	src := iotest.OneByteReader(strings.NewReader(strings.Repeat("x", 1000)))
	pr := newProgressReader(src, tr, 400)
	if _, err := io.Copy(io.Discard, pr); err != nil {
		t.Fatalf("copy: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// offsets[0] is the initial Subscribe snapshot (0). The last reported offset
	// must be the full size; intermediate thresholds must appear and be monotonic.
	if offsets[len(offsets)-1] != 1000 {
		t.Errorf("final offset=%d want 1000", offsets[len(offsets)-1])
	}
	var prev int64 = -1
	sawIntermediate := false
	for _, o := range offsets {
		if o < prev {
			t.Errorf("offsets not monotonic: %v", offsets)
		}
		if o > 0 && o < 1000 {
			sawIntermediate = true
		}
		prev = o
	}
	if !sawIntermediate {
		t.Errorf("expected at least one intermediate threshold report, got %v", offsets)
	}
}

// --- test doubles ---

type failingStore struct{ err error }

func (f failingStore) PutStream(_ context.Context, _ string, r io.Reader, _ int64, _ objectstore.PutOptions) (objectstore.Object, error) {
	_, _ = io.Copy(io.Discard, r) // drain so the progress reader runs
	return objectstore.Object{}, f.err
}

type fakeManager struct {
	tracker     *transfer.Tracker
	beginErr    error
	completeErr error
	failErr     error
}

func (m fakeManager) Begin(_ context.Context, in transfer.BeginInput) (*transfer.Tracker, error) {
	if m.beginErr != nil {
		return nil, m.beginErr
	}
	if m.tracker != nil {
		return m.tracker, nil
	}
	tr, err := transfer.NewTracker(in.TransferID, in.CorrelationID, 0)
	if err != nil {
		return nil, err
	}
	return tr, nil
}

func (m fakeManager) Complete(_ context.Context, _, _ string) error { return m.completeErr }
func (m fakeManager) Fail(_ context.Context, _, _ string) error     { return m.failErr }

func decodeTransferResponse(t *testing.T, rec *httptest.ResponseRecorder) TransferResponse {
	t.Helper()
	var resp TransferResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	return resp
}
