package bulk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
	apperrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/kernellane"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestManagerStreamsChunksCompletesManifestCachesAndReadsRange(t *testing.T) {
	mgr, store, cache, bus := newTestManager(t)
	ctx := bulkContext("org_1", "corr_bulk_1", "idem_1")

	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:     "transfer_1",
		TotalSize:      10,
		ChunkSize:      5,
		MaxMemory:      5,
		IdempotencyKey: "idem_1",
		Deadline:       fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}
	if plan.OrganizationID != "org_1" || plan.CorrelationID != "corr_bulk_1" {
		t.Fatalf("plan scope/correlation = %+v", plan)
	}

	first := acceptTestPart(t, mgr, ctx, plan.TransferID, 0, 0, "hello")
	second := acceptTestPart(t, mgr, ctx, plan.TransferID, 1, 5, "world")
	if first.ObjectKey == second.ObjectKey || first.RawSize != 5 || second.RawSize != 5 {
		t.Fatalf("unexpected receipts: %+v %+v", first, second)
	}

	root := manifestRoot([]PartReceipt{first, second})
	manifest, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: root})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if manifest.RootSHA256 != root || manifest.TotalSize != 10 || len(manifest.Parts) != 2 {
		t.Fatalf("manifest = %+v", manifest)
	}

	rawManifest, err := store.ReadBytes(ctx, manifest.ManifestKey)
	if err != nil {
		t.Fatalf("manifest object missing: %v", err)
	}
	var stored TransferManifest
	if err := json.Unmarshal(rawManifest, &stored); err != nil || stored.RootSHA256 != root {
		t.Fatalf("stored manifest root = %q err=%v", stored.RootSHA256, err)
	}

	reader, desc, err := mgr.OpenRange(ctx, plan.TransferID, 3, 4)
	if err != nil {
		t.Fatalf("OpenRange() error = %v", err)
	}
	payload, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(payload) != "lowo" || len(desc.Parts) != 2 {
		t.Fatalf("range payload = %q desc=%+v err=%v", string(payload), desc, err)
	}

	var walked bytes.Buffer
	walkDesc, err := mgr.ForEachRange(ctx, plan.TransferID, 3, 4, func(part RangePart) error {
		if part.Reader == nil || part.Length <= 0 {
			t.Fatalf("range part = %+v", part)
		}
		_, copyErr := io.Copy(&walked, part.Reader)
		return copyErr
	})
	if err != nil {
		t.Fatalf("ForEachRange() error = %v", err)
	}
	if walked.String() != "lowo" || len(walkDesc.Parts) != 2 {
		t.Fatalf("walked range = %q desc=%+v", walked.String(), walkDesc)
	}

	if cached, err := cache.Get(ctx, "bulk:org_1:transfer_1:part:0"); err != nil || !bytes.Contains(cached, []byte(`"raw_size":5`)) {
		t.Fatalf("cached receipt = %s err=%v", string(cached), err)
	}
	if recent := bus.Recent(20); !hasEvent(recent, "bulk:transfer:complete:v1:success", "corr_bulk_1") {
		t.Fatalf("recent events missing completion success: %+v", recent)
	}
}

func TestManagerRejectsTenantMismatchAndCrossTenantAccess(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	ctx := bulkContext("org_1", "corr_bulk_2", "idem_2")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "transfer_tenant",
		TotalSize:  4,
		ChunkSize:  4,
		MaxMemory:  4,
		Deadline:   fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}

	crossTenant := bulkContext("org_2", "corr_bulk_3", "")
	if _, err := mgr.Complete(crossTenant, plan.TransferID, CompleteRequest{}); !apperrors.Is(err, apperrors.CodeNotFound) {
		t.Fatalf("cross-tenant Complete() error = %v", err)
	}

	mismatch := security.ContextWithOrganizationID(context.Background(), "org_1")
	md := metadata.New()
	md.CorrelationID = "corr_mismatch"
	md.GlobalContext = &metadata.GlobalContext{OrganizationID: "org_2"}
	mismatch = metadata.IntoContext(mismatch, md)
	if _, err := mgr.Initiate(mismatch, InitiateRequest{TransferID: "bad", TotalSize: 0}); !apperrors.Is(err, apperrors.CodeForbidden) {
		t.Fatalf("metadata/auth mismatch error = %v", err)
	}
}

func TestManagerBoundsHashAndCompletionInvariants(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	ctx := bulkContext("org_1", "corr_bulk_4", "")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "transfer_bounds",
		TotalSize:  4,
		ChunkSize:  2,
		MaxMemory:  2,
		Deadline:   fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}

	oversize := PartDescriptor{PartNumber: 0, Offset: 0, Size: 3, ExpectedRawSHA256: shaHex("abc")}
	if _, err := mgr.AcceptPart(ctx, plan.TransferID, oversize, strings.NewReader("abc")); !apperrors.Is(err, apperrors.CodeQuotaExceeded) {
		t.Fatalf("oversize AcceptPart() error = %v", err)
	}

	wrongHash := PartDescriptor{PartNumber: 0, Offset: 0, Size: 2, ExpectedRawSHA256: shaHex("xx")}
	if _, err := mgr.AcceptPart(ctx, plan.TransferID, wrongHash, strings.NewReader("ab")); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("wrong hash AcceptPart() error = %v", err)
	}

	first := acceptTestPart(t, mgr, ctx, plan.TransferID, 0, 0, "ab")
	if _, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot([]PartReceipt{first})}); !apperrors.Is(err, apperrors.CodePrecondition) {
		t.Fatalf("incomplete Complete() error = %v", err)
	}

	second := acceptTestPart(t, mgr, ctx, plan.TransferID, 1, 2, "cd")
	if _, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: shaHex("wrong-root")}); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("wrong root Complete() error = %v", err)
	}
	if _, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot([]PartReceipt{first, second})}); err != nil {
		t.Fatalf("final Complete() error = %v", err)
	}
}

func TestManagerIdempotentPartReplayDoesNotReadBody(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	ctx := bulkContext("org_1", "corr_bulk_5", "")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "transfer_replay",
		TotalSize:  2,
		ChunkSize:  2,
		MaxMemory:  2,
		Deadline:   fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}
	desc := PartDescriptor{PartNumber: 0, Offset: 0, Size: 2, ExpectedRawSHA256: shaHex("ok")}
	first, err := mgr.AcceptPart(ctx, plan.TransferID, desc, strings.NewReader("ok"))
	if err != nil {
		t.Fatalf("AcceptPart() error = %v", err)
	}
	replayed, err := mgr.AcceptPart(ctx, plan.TransferID, desc, errReader{})
	if err != nil {
		t.Fatalf("replayed AcceptPart() error = %v", err)
	}
	if !replayed.IdempotentReplay || replayed.RawSHA256 != first.RawSHA256 {
		t.Fatalf("replayed receipt = %+v first=%+v", replayed, first)
	}
	if _, err := mgr.AcceptPart(ctx, plan.TransferID, PartDescriptor{PartNumber: 0, Offset: 0, Size: 2, ExpectedRawSHA256: shaHex("no")}, errReader{}); !apperrors.Is(err, apperrors.CodeConflict) {
		t.Fatalf("wrong replay hash error = %v", err)
	}
	if _, err := mgr.AcceptPart(ctx, plan.TransferID, PartDescriptor{PartNumber: 0, Offset: 0, Size: 1, ExpectedRawSHA256: shaHex("ok")}, errReader{}); !apperrors.Is(err, apperrors.CodeConflict) {
		t.Fatalf("wrong replay size error = %v", err)
	}
}

func TestManagerCompressedChunksCompleteButRejectRawRange(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	ctx := bulkContext("org_1", "corr_bulk_6", "")
	for _, encoding := range []string{EncodingGzip, EncodingBrotli, EncodingZstd} {
		t.Run(encoding, func(t *testing.T) {
			transferID := "transfer_" + strings.ReplaceAll(encoding, "z", "z_")
			plan, err := mgr.Initiate(ctx, InitiateRequest{
				TransferID:  transferID,
				TotalSize:   6,
				ChunkSize:   6,
				MaxMemory:   6,
				Compression: encoding,
				Deadline:    fixedNow().Add(time.Minute),
			})
			if err != nil {
				t.Fatalf("Initiate() error = %v", err)
			}
			receipt := acceptTestPart(t, mgr, ctx, plan.TransferID, 0, 0, "aaaaaa")
			if receipt.Encoding != encoding || receipt.EncodedSize <= 0 || receipt.EncodedSHA256 == receipt.RawSHA256 {
				t.Fatalf("compressed receipt = %+v", receipt)
			}
			manifest, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot([]PartReceipt{receipt})})
			if err != nil {
				t.Fatalf("Complete() error = %v", err)
			}
			if _, _, err := mgr.OpenRange(ctx, manifest.TransferID, 0, 1); !apperrors.Is(err, apperrors.CodeNotImplemented) {
				t.Fatalf("compressed OpenRange() error = %v", err)
			}
		})
	}

	oversizedPlan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:  "transfer_gzip_oversize",
		TotalSize:   2,
		ChunkSize:   2,
		MaxMemory:   2,
		Compression: EncodingGzip,
		Deadline:    fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("oversized gzip Initiate() error = %v", err)
	}
	desc := PartDescriptor{PartNumber: 0, Size: 2, ExpectedRawSHA256: shaHex("aa")}
	if _, err := mgr.AcceptPart(ctx, oversizedPlan.TransferID, desc, strings.NewReader("aaa")); !apperrors.Is(err, apperrors.CodeQuotaExceeded) {
		t.Fatalf("oversized gzip AcceptPart() error = %v", err)
	}
}

func TestManagerAutoCompressionKeepsIncompressibleChunksIdentity(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://bulk-auto-tests", Bucket: "bulk"})
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		DefaultChunkSize: 256,
		MaxChunkSize:     256,
		MaxParts:         4,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_bulk_auto", "")
	compressible := strings.Repeat("foundation-", 16)
	compressiblePlan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:  "transfer_auto_compressible",
		TotalSize:   int64(len(compressible)),
		ChunkSize:   int64(len(compressible)),
		MaxMemory:   int64(len(compressible)),
		Compression: EncodingAuto,
		Deadline:    fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("compressible Initiate() error = %v", err)
	}
	compressed := acceptTestPart(t, mgr, ctx, compressiblePlan.TransferID, 0, 0, compressible)
	if compressed.Encoding == EncodingIdentity || compressed.EncodedSize >= compressed.RawSize {
		t.Fatalf("auto compressible receipt = %+v", compressed)
	}

	incompressible := "\x00\x01\x02\x03\x04\x05\x06\x07"
	incompressiblePlan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:  "transfer_auto_incompressible",
		TotalSize:   int64(len(incompressible)),
		ChunkSize:   int64(len(incompressible)),
		MaxMemory:   int64(len(incompressible)),
		Compression: EncodingAuto,
		Deadline:    fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("incompressible Initiate() error = %v", err)
	}
	identity := acceptTestPart(t, mgr, ctx, incompressiblePlan.TransferID, 0, 0, incompressible)
	if identity.Encoding != EncodingIdentity || identity.EncodedSize != identity.RawSize {
		t.Fatalf("auto incompressible receipt = %+v", identity)
	}
}

func TestNewManagerDefaultsAutoIDAndInitiateReplay(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://bulk-tests", Bucket: "bulk"})
	if _, err := NewManager(Options{}); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("nil store NewManager() error = %v", err)
	}
	mgr, err := NewManager(Options{ObjectStore: store, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_bulk_7", "idem_replay")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TotalSize:      1,
		IdempotencyKey: "idem_replay",
		Attributes:     map[string]string{"purpose": "test"},
	})
	if err != nil {
		t.Fatalf("auto Initiate() error = %v", err)
	}
	if plan.TransferID == "" || plan.ChunkSize != DefaultChunkSize || plan.Attributes["purpose"] != "test" {
		t.Fatalf("default plan = %+v", plan)
	}

	duplicate, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:     plan.TransferID,
		TotalSize:      1,
		IdempotencyKey: "idem_replay",
	})
	if err != nil || duplicate.TransferID != plan.TransferID {
		t.Fatalf("idempotent Initiate() = %+v err=%v", duplicate, err)
	}
	if _, err := mgr.Initiate(ctx, InitiateRequest{TransferID: plan.TransferID, TotalSize: 1, IdempotencyKey: "other"}); !apperrors.Is(err, apperrors.CodeConflict) {
		t.Fatalf("conflicting Initiate() error = %v", err)
	}
}

func TestInitiateValidationBoundaries(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	ctx := bulkContext("org_1", "corr_bulk_8", "")
	tests := []struct {
		name string
		req  InitiateRequest
		code apperrors.Code
	}{
		{"negative total", InitiateRequest{TransferID: "v1", TotalSize: -1}, apperrors.CodeValidation},
		{"chunk too large", InitiateRequest{TransferID: "v2", TotalSize: 1, ChunkSize: 9}, apperrors.CodeValidation},
		{"memory too large", InitiateRequest{TransferID: "v3", TotalSize: 1, MaxMemory: 9}, apperrors.CodeValidation},
		{"chunk exceeds memory", InitiateRequest{TransferID: "v4", TotalSize: 1, ChunkSize: 4, MaxMemory: 2}, apperrors.CodeValidation},
		{"part budget", InitiateRequest{TransferID: "v5", TotalSize: 17, ChunkSize: 5, MaxParts: 3}, apperrors.CodeQuotaExceeded},
		{"expired", InitiateRequest{TransferID: "v6", TotalSize: 1, Deadline: fixedNow().Add(-time.Second)}, apperrors.CodeExpired},
		{"compression", InitiateRequest{TransferID: "v7", TotalSize: 1, Compression: "deflate"}, apperrors.CodeValidation},
		{"transfer id", InitiateRequest{TransferID: "bad id", TotalSize: 1}, apperrors.CodeValidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := mgr.Initiate(ctx, tt.req); !apperrors.Is(err, tt.code) {
				t.Fatalf("Initiate() error = %v want %s", err, tt.code)
			}
		})
	}
	if _, err := mgr.Initiate(context.Background(), InitiateRequest{TotalSize: 1}); !apperrors.Is(err, apperrors.CodeUnauthorized) {
		t.Fatalf("missing auth Initiate() error = %v", err)
	}
}

func TestAcceptPartValidationAndStateBoundaries(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	ctx := bulkContext("org_1", "corr_bulk_9", "")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "transfer_accept_bounds",
		TotalSize:  4,
		ChunkSize:  2,
		MaxMemory:  2,
		Deadline:   fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}
	tests := []struct {
		name   string
		desc   PartDescriptor
		reader io.Reader
		code   apperrors.Code
	}{
		{"nil reader", PartDescriptor{PartNumber: 0, Size: 1, ExpectedRawSHA256: shaHex("a")}, nil, apperrors.CodeValidation},
		{"negative part", PartDescriptor{PartNumber: -1, Size: 1, ExpectedRawSHA256: shaHex("a")}, strings.NewReader("a"), apperrors.CodeValidation},
		{"negative offset", PartDescriptor{PartNumber: 0, Offset: -1, Size: 1, ExpectedRawSHA256: shaHex("a")}, strings.NewReader("a"), apperrors.CodeValidation},
		{"negative size", PartDescriptor{PartNumber: 0, Size: -1, ExpectedRawSHA256: shaHex("a")}, strings.NewReader("a"), apperrors.CodeValidation},
		{"offset overflow", PartDescriptor{PartNumber: 0, Offset: maxInt64, Size: 1, ExpectedRawSHA256: shaHex("a")}, strings.NewReader("a"), apperrors.CodeValidation},
		{"exceeds total", PartDescriptor{PartNumber: 0, Offset: 3, Size: 2, ExpectedRawSHA256: shaHex("ab")}, strings.NewReader("ab"), apperrors.CodeValidation},
		{"missing hash", PartDescriptor{PartNumber: 0, Size: 1}, strings.NewReader("a"), apperrors.CodeValidation},
		{"bad hash", PartDescriptor{PartNumber: 0, Size: 1, ExpectedRawSHA256: "not-a-hash"}, strings.NewReader("a"), apperrors.CodeValidation},
		{"reader too long", PartDescriptor{PartNumber: 0, Size: 2, ExpectedRawSHA256: shaHex("ab")}, strings.NewReader("abc"), apperrors.CodeQuotaExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := mgr.AcceptPart(ctx, plan.TransferID, tt.desc, tt.reader); !apperrors.Is(err, tt.code) {
				t.Fatalf("AcceptPart() error = %v want %s", err, tt.code)
			}
		})
	}

	expiredPlan := plan
	expiredPlan.TransferID = "transfer_expired"
	expiredPlan.Deadline = fixedNow().Add(-time.Second)
	if err := mgr.state.SavePlan(ctx, expiredPlan); err != nil {
		t.Fatalf("SavePlan(expired) error = %v", err)
	}
	desc := PartDescriptor{PartNumber: 0, Size: 1, ExpectedRawSHA256: shaHex("a")}
	if _, err := mgr.AcceptPart(ctx, expiredPlan.TransferID, desc, strings.NewReader("a")); !apperrors.Is(err, apperrors.CodeExpired) {
		t.Fatalf("expired AcceptPart() error = %v", err)
	}

	completePlan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "transfer_closed",
		TotalSize:  1,
		ChunkSize:  1,
		MaxMemory:  1,
		Deadline:   fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("closed Initiate() error = %v", err)
	}
	closedReceipt := acceptTestPart(t, mgr, ctx, completePlan.TransferID, 0, 0, "z")
	if _, err := mgr.Complete(ctx, completePlan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot([]PartReceipt{closedReceipt})}); err != nil {
		t.Fatalf("closed Complete() error = %v", err)
	}
	if _, err := mgr.AcceptPart(ctx, completePlan.TransferID, desc, strings.NewReader("a")); !apperrors.Is(err, apperrors.CodeInvalidState) {
		t.Fatalf("closed AcceptPart() error = %v", err)
	}
}

func TestCompleteAndOpenRangeBoundaryFailures(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	ctx := bulkContext("org_1", "corr_bulk_10", "")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "transfer_complete_bounds",
		TotalSize:  4,
		ChunkSize:  4,
		MaxMemory:  4,
		Deadline:   fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}
	if _, _, err := mgr.OpenRange(ctx, plan.TransferID, 0, 1); !apperrors.Is(err, apperrors.CodeNotFound) {
		t.Fatalf("OpenRange without manifest error = %v", err)
	}

	gap := PartReceipt{
		TransferID:     plan.TransferID,
		OrganizationID: plan.OrganizationID,
		PartNumber:     1,
		Offset:         0,
		RawSize:        4,
		RawSHA256:      shaHex("data"),
		EncodedSHA256:  shaHex("data"),
		Encoding:       EncodingIdentity,
		ObjectKey:      plan.ObjectPrefix + "/manual-gap",
	}
	if err := mgr.state.SaveReceipt(ctx, gap); err != nil {
		t.Fatalf("SaveReceipt(gap) error = %v", err)
	}
	if _, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{}); !apperrors.Is(err, apperrors.CodePrecondition) {
		t.Fatalf("gap Complete() error = %v", err)
	}

	emptyPlan, err := mgr.Initiate(ctx, InitiateRequest{TransferID: "empty_transfer", TotalSize: 0})
	if err != nil {
		t.Fatalf("empty Initiate() error = %v", err)
	}
	if _, err := mgr.Complete(ctx, emptyPlan.TransferID, CompleteRequest{}); err != nil {
		t.Fatalf("empty Complete() error = %v", err)
	}
	if _, _, err := mgr.OpenRange(ctx, emptyPlan.TransferID, 0, 1); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("empty OpenRange() error = %v", err)
	}

	rangePlan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "transfer_second_part_range",
		TotalSize:  4,
		ChunkSize:  2,
		MaxMemory:  2,
		Deadline:   fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("range Initiate() error = %v", err)
	}
	r0 := acceptTestPart(t, mgr, ctx, rangePlan.TransferID, 0, 0, "ab")
	r1 := acceptTestPart(t, mgr, ctx, rangePlan.TransferID, 1, 2, "cd")
	if _, err := mgr.Complete(ctx, rangePlan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot([]PartReceipt{r0, r1})}); err != nil {
		t.Fatalf("range Complete() error = %v", err)
	}
	if _, _, err := mgr.OpenRange(ctx, rangePlan.TransferID, maxInt64, 1); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("overflow OpenRange() error = %v", err)
	}
	if _, err := mgr.ForEachRange(ctx, rangePlan.TransferID, maxInt64, 1, func(RangePart) error { return nil }); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("overflow ForEachRange() error = %v", err)
	}
	reader, desc, err := mgr.OpenRange(ctx, rangePlan.TransferID, 2, 2)
	if err != nil {
		t.Fatalf("second part OpenRange() error = %v", err)
	}
	body, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(body) != "cd" || len(desc.Parts) != 1 || desc.Parts[0] != 1 {
		t.Fatalf("second part range = %q desc=%+v err=%v", string(body), desc, err)
	}
	firstReader, firstDesc, err := mgr.OpenRange(ctx, rangePlan.TransferID, 0, 2)
	if err != nil {
		t.Fatalf("first part OpenRange() error = %v", err)
	}
	firstBody, err := io.ReadAll(firstReader)
	_ = firstReader.Close()
	if err != nil || string(firstBody) != "ab" || len(firstDesc.Parts) != 1 || firstDesc.Parts[0] != 0 {
		t.Fatalf("first part range = %q desc=%+v err=%v", string(firstBody), firstDesc, err)
	}
	badManifest := TransferManifest{
		TransferID:     "bad_manifest_range",
		OrganizationID: "org_1",
		TotalSize:      1,
		Parts: []PartReceipt{{
			TransferID:     "bad_manifest_range",
			OrganizationID: "org_1",
			PartNumber:     0,
			RawSize:        1,
			Encoding:       EncodingIdentity,
			ObjectKey:      "missing-object",
		}},
	}
	badPlan := rangePlan
	badPlan.TransferID = badManifest.TransferID
	badPlan.TotalSize = 1
	if err := mgr.state.SavePlan(ctx, badPlan); err != nil {
		t.Fatalf("SavePlan(bad range) error = %v", err)
	}
	if err := mgr.state.SaveManifest(ctx, badManifest); err != nil {
		t.Fatalf("SaveManifest(bad range) error = %v", err)
	}
	if _, _, err := mgr.OpenRange(ctx, badManifest.TransferID, 0, 1); err == nil {
		t.Fatal("expected missing object range to fail")
	}
	unresolved := badManifest
	unresolved.TransferID = "unresolved_range"
	unresolved.Parts = nil
	unresolvedPlan := badPlan
	unresolvedPlan.TransferID = unresolved.TransferID
	if err := mgr.state.SavePlan(ctx, unresolvedPlan); err != nil {
		t.Fatalf("SavePlan(unresolved range) error = %v", err)
	}
	if err := mgr.state.SaveManifest(ctx, unresolved); err != nil {
		t.Fatalf("SaveManifest(unresolved range) error = %v", err)
	}
	if _, _, err := mgr.OpenRange(ctx, unresolved.TransferID, 0, 1); !apperrors.Is(err, apperrors.CodePrecondition) {
		t.Fatalf("unresolved OpenRange() error = %v", err)
	}
	if _, _, err := mgr.OpenRange(ctx, "", 0, 1); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("empty transfer OpenRange() error = %v", err)
	}
	if _, err := mgr.ForEachRange(ctx, rangePlan.TransferID, 0, 1, nil); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("nil callback ForEachRange() error = %v", err)
	}
	if _, err := mgr.ForEachRange(ctx, rangePlan.TransferID, 0, 1, func(part RangePart) error {
		_, _ = io.Copy(io.Discard, part.Reader)
		return errors.New("stop range")
	}); err == nil {
		t.Fatal("expected ForEachRange callback error")
	}
	compressedRangePlan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:  "compressed_foreach_range",
		TotalSize:   1,
		ChunkSize:   1,
		MaxMemory:   1,
		Compression: EncodingGzip,
	})
	if err != nil {
		t.Fatalf("compressed foreach Initiate() error = %v", err)
	}
	compressedReceipt := acceptTestPart(t, mgr, ctx, compressedRangePlan.TransferID, 0, 0, "a")
	if _, err := mgr.Complete(ctx, compressedRangePlan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot([]PartReceipt{compressedReceipt})}); err != nil {
		t.Fatalf("compressed foreach Complete() error = %v", err)
	}
	if _, err := mgr.ForEachRange(ctx, compressedRangePlan.TransferID, 0, 1, func(part RangePart) error {
		return nil
	}); !apperrors.Is(err, apperrors.CodeNotImplemented) {
		t.Fatalf("compressed ForEachRange() error = %v", err)
	}
}

func TestDependencyFailuresAreTyped(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://bulk-tests", Bucket: "bulk"})
	ctx := bulkContext("org_1", "corr_bulk_11", "")
	busMgr, err := NewManager(Options{ObjectStore: store, EventBus: failingBus{}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(failing bus) error = %v", err)
	}
	if _, err := busMgr.Initiate(ctx, InitiateRequest{TransferID: "event_fail", TotalSize: 1}); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("event failure Initiate() error = %v", err)
	}

	objectMgr, err := NewManager(Options{ObjectStore: failingObjectStore{}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(failing object) error = %v", err)
	}
	plan, err := objectMgr.Initiate(ctx, InitiateRequest{TransferID: "object_fail", TotalSize: 1, ChunkSize: 1, MaxMemory: 1})
	if err != nil {
		t.Fatalf("Initiate() with failing object store error = %v", err)
	}
	desc := PartDescriptor{PartNumber: 0, Size: 1, ExpectedRawSHA256: shaHex("a")}
	if _, err := objectMgr.AcceptPart(ctx, plan.TransferID, desc, strings.NewReader("a")); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("object failure AcceptPart() error = %v", err)
	}

	compressedObjectMgr, err := NewManager(Options{ObjectStore: failingObjectStore{}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(failing compressed object) error = %v", err)
	}
	compressedPlan, err := compressedObjectMgr.Initiate(ctx, InitiateRequest{
		TransferID:  "compressed_object_fail",
		TotalSize:   128,
		ChunkSize:   128,
		MaxMemory:   128,
		Compression: EncodingGzip,
	})
	if err != nil {
		t.Fatalf("compressed object fail Initiate() error = %v", err)
	}
	if _, err := compressedObjectMgr.AcceptPart(ctx, compressedPlan.TransferID, PartDescriptor{
		PartNumber:        0,
		Size:              128,
		ExpectedRawSHA256: shaHex(strings.Repeat("a", 128)),
	}, strings.NewReader(strings.Repeat("a", 128))); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("compressed object failure AcceptPart() error = %v", err)
	}

	autoObjectMgr, err := NewManager(Options{ObjectStore: failingObjectStore{}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(failing auto object) error = %v", err)
	}
	autoPlan, err := autoObjectMgr.Initiate(ctx, InitiateRequest{
		TransferID:  "auto_object_fail",
		TotalSize:   128,
		ChunkSize:   128,
		MaxMemory:   128,
		Compression: EncodingAuto,
	})
	if err != nil {
		t.Fatalf("auto object fail Initiate() error = %v", err)
	}
	if _, err := autoObjectMgr.AcceptPart(ctx, autoPlan.TransferID, PartDescriptor{
		PartNumber:        0,
		Size:              128,
		ExpectedRawSHA256: shaHex(strings.Repeat("b", 128)),
	}, strings.NewReader(strings.Repeat("b", 128))); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("auto object failure AcceptPart() error = %v", err)
	}

	savePlanMgr, err := NewManager(Options{ObjectStore: store, StateStore: savePlanFailState{StateStore: NewMemoryStateStore()}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(save plan fail) error = %v", err)
	}
	if _, err := savePlanMgr.Initiate(ctx, InitiateRequest{TransferID: "save_plan_fail", TotalSize: 1}); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("save plan failure Initiate() error = %v", err)
	}

	saveReceiptMgr, err := NewManager(Options{ObjectStore: store, StateStore: saveReceiptFailState{StateStore: NewMemoryStateStore()}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(save receipt fail) error = %v", err)
	}
	receiptPlan, err := saveReceiptMgr.Initiate(ctx, InitiateRequest{TransferID: "save_receipt_fail", TotalSize: 1, ChunkSize: 1, MaxMemory: 1})
	if err != nil {
		t.Fatalf("save receipt Initiate() error = %v", err)
	}
	if _, err := saveReceiptMgr.AcceptPart(ctx, receiptPlan.TransferID, desc, strings.NewReader("a")); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("save receipt failure AcceptPart() error = %v", err)
	}

	signedReceiptMgr, err := NewManager(Options{ObjectStore: store, StateStore: saveReceiptFailState{StateStore: NewMemoryStateStore()}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(signed receipt fail) error = %v", err)
	}
	signedReceiptPlan, err := signedReceiptMgr.Initiate(ctx, InitiateRequest{TransferID: "signed_receipt_fail", TotalSize: 1, ChunkSize: 1, MaxMemory: 1})
	if err != nil {
		t.Fatalf("signed receipt Initiate() error = %v", err)
	}
	if _, err := store.PutStream(ctx, partObjectKey(signedReceiptPlan, 0), strings.NewReader("a"), 1, objectstore.PutOptions{}); err != nil {
		t.Fatalf("signed direct put error = %v", err)
	}
	if _, err := signedReceiptMgr.AcceptSignedPart(ctx, signedReceiptPlan.TransferID, desc); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("save receipt failure AcceptSignedPart() error = %v", err)
	}

	manifestStore := manifestFailStore{Store: store}
	manifestMgr, err := NewManager(Options{ObjectStore: manifestStore, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(manifest store) error = %v", err)
	}
	manifestPlan, err := manifestMgr.Initiate(ctx, InitiateRequest{TransferID: "manifest_fail", TotalSize: 1, ChunkSize: 1, MaxMemory: 1})
	if err != nil {
		t.Fatalf("manifest Initiate() error = %v", err)
	}
	manifestReceipt := acceptTestPart(t, manifestMgr, ctx, manifestPlan.TransferID, 0, 0, "a")
	if _, err := manifestMgr.Complete(ctx, manifestPlan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot([]PartReceipt{manifestReceipt})}); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("manifest store failure Complete() error = %v", err)
	}

	saveManifestMgr, err := NewManager(Options{ObjectStore: store, StateStore: saveManifestFailState{StateStore: NewMemoryStateStore()}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(save manifest fail) error = %v", err)
	}
	saveManifestPlan, err := saveManifestMgr.Initiate(ctx, InitiateRequest{TransferID: "save_manifest_fail", TotalSize: 1, ChunkSize: 1, MaxMemory: 1})
	if err != nil {
		t.Fatalf("save manifest Initiate() error = %v", err)
	}
	saveManifestReceipt := acceptTestPart(t, saveManifestMgr, ctx, saveManifestPlan.TransferID, 0, 0, "a")
	if _, err := saveManifestMgr.Complete(ctx, saveManifestPlan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot([]PartReceipt{saveManifestReceipt})}); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("save manifest failure Complete() error = %v", err)
	}

	listFailMgr, err := NewManager(Options{ObjectStore: store, StateStore: listReceiptsFailState{StateStore: NewMemoryStateStore()}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(list fail) error = %v", err)
	}
	listPlan, err := listFailMgr.Initiate(ctx, InitiateRequest{TransferID: "list_fail", TotalSize: 0})
	if err != nil {
		t.Fatalf("list fail Initiate() error = %v", err)
	}
	if _, err := listFailMgr.Complete(ctx, listPlan.TransferID, CompleteRequest{}); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("list receipts failure Complete() error = %v", err)
	}

	statusListFailMgr, err := NewManager(Options{ObjectStore: store, StateStore: listReceiptsFailState{StateStore: NewMemoryStateStore()}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(status list fail) error = %v", err)
	}
	statusPlan, err := statusListFailMgr.Initiate(ctx, InitiateRequest{TransferID: "status_list_fail", TotalSize: 1})
	if err != nil {
		t.Fatalf("status list fail Initiate() error = %v", err)
	}
	if _, err := statusListFailMgr.Status(ctx, statusPlan.TransferID); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("list receipts failure Status() error = %v", err)
	}

	statusManifestFailMgr, err := NewManager(Options{ObjectStore: store, StateStore: loadManifestFailState{StateStore: NewMemoryStateStore()}, Clock: fixedNow})
	if err != nil {
		t.Fatalf("NewManager(status manifest fail) error = %v", err)
	}
	manifestStatusPlan, err := statusManifestFailMgr.Initiate(ctx, InitiateRequest{TransferID: "status_manifest_fail", TotalSize: 1})
	if err != nil {
		t.Fatalf("status manifest fail Initiate() error = %v", err)
	}
	if _, err := statusManifestFailMgr.Status(ctx, manifestStatusPlan.TransferID); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("manifest load failure Status() error = %v", err)
	}
}

func TestStatusReportsEmptyAndCompletedTransfers(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	ctx := bulkContext("org_1", "corr_bulk_status", "")
	empty, err := mgr.Initiate(ctx, InitiateRequest{TransferID: "status_empty", TotalSize: 0})
	if err != nil {
		t.Fatalf("empty Initiate() error = %v", err)
	}
	emptyStatus, err := mgr.Status(ctx, empty.TransferID)
	if err != nil {
		t.Fatalf("empty Status() error = %v", err)
	}
	if emptyStatus.PartsAccepted != 0 || len(emptyStatus.MissingParts) != 0 || emptyStatus.ResumeToken == "" {
		t.Fatalf("empty status = %+v", emptyStatus)
	}

	plan, err := mgr.Initiate(ctx, InitiateRequest{TransferID: "status_done", TotalSize: 1, ChunkSize: 1, MaxMemory: 1})
	if err != nil {
		t.Fatalf("done Initiate() error = %v", err)
	}
	receipt := acceptTestPart(t, mgr, ctx, plan.TransferID, 0, 0, "d")
	root := manifestRoot([]PartReceipt{receipt})
	if _, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: root}); err != nil {
		t.Fatalf("Complete(status_done) error = %v", err)
	}
	done, err := mgr.Status(ctx, plan.TransferID)
	if err != nil {
		t.Fatalf("done Status() error = %v", err)
	}
	if done.State != StateCompleted || done.RootSHA256 != root || done.ManifestKey == "" || len(done.MissingParts) != 0 {
		t.Fatalf("completed status = %+v", done)
	}
}

func TestManagerReadMutationAndGrantBoundaries(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://bulk-boundary-extra", Bucket: "bulk"})
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		DefaultChunkSize: 2,
		MaxChunkSize:     2,
		MaxParts:         2,
		ReceiptTTL:       time.Minute,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_boundary_extra", "")
	if _, err := mgr.Status(ctx, ""); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("empty Status() error = %v", err)
	}
	if _, err := mgr.ForEachRange(context.Background(), "missing", 0, 1, func(RangePart) error { return nil }); !apperrors.Is(err, apperrors.CodeUnauthorized) {
		t.Fatalf("unauthorized ForEachRange() error = %v", err)
	}
	plan, err := mgr.Initiate(ctx, InitiateRequest{TransferID: "boundary_extra", TotalSize: 2, ChunkSize: 2, MaxMemory: 2, ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}
	grant, err := mgr.GrantSignedPart(ctx, SignedPartRequest{
		TransferID: plan.TransferID,
		Descriptor: PartDescriptor{
			PartNumber:        0,
			Size:              2,
			ExpectedRawSHA256: shaHex("ok"),
		},
		Expiry: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("GrantSignedPart() error = %v", err)
	}
	if grant.ContentType != "text/plain" || !grant.ExpiresAt.Equal(fixedNow().Add(time.Minute)) {
		t.Fatalf("grant defaults = %+v", grant)
	}
}

func TestAcceptSignedPartEmitsSuccessFailureAsDependency(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://bulk-signed-event-fail", Bucket: "bulk"})
	state := NewMemoryStateStore()
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		StateStore:       state,
		EventBus:         failingBus{},
		DefaultChunkSize: 1,
		MaxChunkSize:     1,
		MaxParts:         1,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_signed_event_fail", "")
	plan := TransferPlan{
		TransferID:     "signed_event_fail",
		OrganizationID: "org_1",
		CorrelationID:  "corr_signed_event_fail",
		TotalSize:      1,
		ChunkSize:      1,
		MaxMemory:      1,
		MaxParts:       1,
		ContentType:    "application/octet-stream",
		Compression:    EncodingIdentity,
		ObjectPrefix:   "bulk/org_1/signed_event_fail",
		ManifestKey:    "bulk/org_1/signed_event_fail/manifest.json",
		State:          StateInitiated,
		CreatedAt:      fixedNow(),
	}
	if err := state.SavePlan(ctx, plan); err != nil {
		t.Fatalf("SavePlan() error = %v", err)
	}
	if _, err := store.PutStream(ctx, partObjectKey(plan, 0), strings.NewReader("x"), 1, objectstore.PutOptions{}); err != nil {
		t.Fatalf("PutStream() error = %v", err)
	}
	if _, err := mgr.AcceptSignedPart(ctx, plan.TransferID, PartDescriptor{
		PartNumber:        0,
		Size:              1,
		ExpectedRawSHA256: shaHex("x"),
	}); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("AcceptSignedPart event failure error = %v", err)
	}
}

func TestCompleteEmitsSuccessFailureAsDependency(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://bulk-complete-event-fail", Bucket: "bulk"})
	state := NewMemoryStateStore()
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		StateStore:       state,
		EventBus:         failingBus{},
		DefaultChunkSize: 1,
		MaxChunkSize:     1,
		MaxParts:         1,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_complete_event_fail", "")
	plan := TransferPlan{
		TransferID:     "complete_event_fail",
		OrganizationID: "org_1",
		CorrelationID:  "corr_complete_event_fail",
		TotalSize:      1,
		ChunkSize:      1,
		MaxMemory:      1,
		MaxParts:       1,
		ContentType:    "application/octet-stream",
		Compression:    EncodingIdentity,
		ObjectPrefix:   "bulk/org_1/complete_event_fail",
		ManifestKey:    "bulk/org_1/complete_event_fail/manifest.json",
		State:          StateInitiated,
		CreatedAt:      fixedNow(),
	}
	receipt := PartReceipt{
		TransferID:     plan.TransferID,
		OrganizationID: plan.OrganizationID,
		CorrelationID:  plan.CorrelationID,
		PartNumber:     0,
		RawSize:        1,
		RawSHA256:      shaHex("x"),
		EncodedSHA256:  shaHex("x"),
		Encoding:       EncodingIdentity,
		ObjectKey:      partObjectKey(plan, 0),
		CreatedAt:      fixedNow(),
	}
	if err := state.SavePlan(ctx, plan); err != nil {
		t.Fatalf("SavePlan() error = %v", err)
	}
	if err := state.SaveReceipt(ctx, receipt); err != nil {
		t.Fatalf("SaveReceipt() error = %v", err)
	}
	if _, err := store.PutStream(ctx, receipt.ObjectKey, strings.NewReader("x"), 1, objectstore.PutOptions{}); err != nil {
		t.Fatalf("PutStream() error = %v", err)
	}
	if _, err := mgr.Complete(ctx, plan.TransferID, CompleteRequest{ExpectedRootSHA256: manifestRoot([]PartReceipt{receipt})}); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("Complete event failure error = %v", err)
	}
}

func TestAcceptPartEmitsSuccessFailureAsDependency(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://bulk-accept-event-fail", Bucket: "bulk"})
	state := NewMemoryStateStore()
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		StateStore:       state,
		EventBus:         &failAfterFirstBus{},
		DefaultChunkSize: 1,
		MaxChunkSize:     1,
		MaxParts:         1,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_accept_event_fail", "")
	plan := TransferPlan{
		TransferID:     "accept_event_fail",
		OrganizationID: "org_1",
		CorrelationID:  "corr_accept_event_fail",
		TotalSize:      1,
		ChunkSize:      1,
		MaxMemory:      1,
		MaxParts:       1,
		ContentType:    "application/octet-stream",
		Compression:    EncodingIdentity,
		ObjectPrefix:   "bulk/org_1/accept_event_fail",
		ManifestKey:    "bulk/org_1/accept_event_fail/manifest.json",
		State:          StateInitiated,
		CreatedAt:      fixedNow(),
	}
	if err := state.SavePlan(ctx, plan); err != nil {
		t.Fatalf("SavePlan() error = %v", err)
	}
	if _, err := mgr.AcceptPart(ctx, plan.TransferID, PartDescriptor{
		PartNumber:        0,
		Size:              1,
		ExpectedRawSHA256: shaHex("x"),
	}, strings.NewReader("x")); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("AcceptPart event failure error = %v", err)
	}
}

func TestCompressionHelpersBoundMemoryAndEncoding(t *testing.T) {
	if _, _, err := readBoundedPart(strings.NewReader("x"), -1); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("negative bounded read error = %v", err)
	}
	if _, _, err := readBoundedPart(strings.NewReader("x"), 2); err == nil {
		t.Fatal("expected short bounded read to fail")
	}
	if _, _, err := readBoundedPart(strings.NewReader("abc"), 2); !apperrors.Is(err, apperrors.CodeQuotaExceeded) {
		t.Fatalf("oversized bounded read error = %v", err)
	}
	payload, digest, err := readBoundedPart(strings.NewReader("ok"), 2)
	if err != nil || string(payload) != "ok" || digest != shaHex("ok") {
		t.Fatalf("bounded read payload=%q digest=%s err=%v", string(payload), digest, err)
	}
	if _, err := compressBytes([]byte("payload"), "deflate"); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("unsupported compression error = %v", err)
	}
	reader, _, result, err := compressedPartReader(strings.NewReader("payload"), 7, "deflate")
	if !apperrors.Is(err, apperrors.CodeValidation) || reader != nil {
		t.Fatalf("unsupported compressed reader = reader:%v err:%v", reader, err)
	}
	select {
	case _, ok := <-result:
		if ok {
			t.Fatal("unsupported compressed reader result channel should be closed")
		}
	default:
		t.Fatal("unsupported compressed reader result channel was not closed")
	}
	identity, encoding, err := compressAuto([]byte("short"))
	if err != nil || encoding != EncodingIdentity || string(identity) != "short" {
		t.Fatalf("short auto compression encoding=%s payload=%q err=%v", encoding, string(identity), err)
	}
	compressed, encoding, err := compressAuto(bytes.Repeat([]byte("a"), 512))
	if err != nil || encoding != EncodingGzip || len(compressed) >= 512 {
		t.Fatalf("compressible auto compression encoding=%s len=%d err=%v", encoding, len(compressed), err)
	}
}

func TestSmallHelpersCoverFallbackBranches(t *testing.T) {
	if got := manifestRoot([]PartReceipt{{RawSHA256: "not-hex"}}); got != shaHex("not-hex") {
		t.Fatalf("manifestRoot fallback = %s", got)
	}
	if ceilDiv(0, 5) != 0 || ceilDiv(6, 5) != 2 {
		t.Fatal("ceilDiv failed")
	}
	if firstPositive(0, -1, 7) != 7 {
		t.Fatal("firstPositive failed")
	}
	if firstPositive(0, -1) != 0 {
		t.Fatal("firstPositive zero fallback failed")
	}
	if firstNonEmpty(" ", " value ") != "value" || firstNonEmpty(" ", "") != "" {
		t.Fatal("firstNonEmpty failed")
	}
	if partObjectKey(TransferPlan{ObjectPrefix: "bulk/org/t"}, 1_000_000) != "bulk/org/t/parts/1000000" {
		t.Fatal("large part object key failed")
	}
	if expectedPartCount(TransferPlan{TotalSize: 100, ChunkSize: 1, MaxParts: 4}) != 4 {
		t.Fatal("expectedPartCount cap failed")
	}
	if err := verifyPartDigest(PartDescriptor{Size: 2, ExpectedRawSHA256: shaHex("a")}, 1, shaHex("a")); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("verifyPartDigest size error = %v", err)
	}
	if err := validateInitiate(InitiateRequest{}, 1, 1, 0, 8, fixedNow()); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("validateInitiate max parts error = %v", err)
	}
	initialized := NewMemoryStateStore()
	initialized.initLocked()
	uninitialized := &MemoryStateStore{}
	uninitialized.initLocked()
	if err := closeReaders([]io.ReadCloser{closeErrorReader{}}); err == nil {
		t.Fatal("expected closeReaders to return close error")
	}
	wrapped := closeReadersWithError([]io.ReadCloser{closeErrorReader{}}, errors.New("primary"))
	if !apperrors.Is(wrapped, apperrors.CodeDependency) || !strings.Contains(wrapped.Error(), "primary") {
		t.Fatalf("closeReadersWithError() = %v", wrapped)
	}
	if cloneOptionalStringMap(map[string]string{"k": "v"})["k"] != "v" {
		t.Fatal("cloneOptionalStringMap non-empty failed")
	}
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://bulk-tests", Bucket: "bulk"})
	mgr, err := NewManager(Options{ObjectStore: store})
	if err != nil || mgr.clock == nil {
		t.Fatalf("default clock manager = %+v err=%v", mgr, err)
	}
	if _, err := mgr.replayInitiate(context.Background(), TransferPlan{OrganizationID: "org_a"}, TransferPlan{OrganizationID: "org_b"}); !apperrors.Is(err, apperrors.CodeForbidden) {
		t.Fatalf("replayInitiate org mismatch error = %v", err)
	}
}

func TestMemoryStateStoreBoundaries(t *testing.T) {
	ctx := context.Background()
	var nilStore *MemoryStateStore
	if err := nilStore.SavePlan(ctx, TransferPlan{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil SavePlan() error = %v", err)
	}
	if _, err := nilStore.LoadPlan(ctx, "org", "t"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil LoadPlan() error = %v", err)
	}
	if err := nilStore.SaveReceipt(ctx, PartReceipt{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil SaveReceipt() error = %v", err)
	}
	if _, err := nilStore.LoadReceipt(ctx, "org", "t", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil LoadReceipt() error = %v", err)
	}
	if _, err := nilStore.ListReceipts(ctx, "org", "t"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil ListReceipts() error = %v", err)
	}
	if err := nilStore.SaveManifest(ctx, TransferManifest{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil SaveManifest() error = %v", err)
	}
	if _, err := nilStore.LoadManifest(ctx, "org", "t"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nil LoadManifest() error = %v", err)
	}

	store := &MemoryStateStore{}
	if receipts, err := store.ListReceipts(ctx, "org", "t"); err != nil || len(receipts) != 0 {
		t.Fatalf("empty ListReceipts() = %+v err=%v", receipts, err)
	}
	if _, err := store.LoadManifest(ctx, "org", "t"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing LoadManifest() error = %v", err)
	}
}

func newTestManager(t *testing.T) (*Manager, *objectstore.Store, redis.Client, *events.InMemoryBus) {
	t.Helper()
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://bulk-tests",
		Bucket:   "bulk",
	})
	cache := redis.NewMemoryClient("test")
	bus := events.NewInMemoryBus(50)
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		Cache:            cache,
		EventBus:         bus,
		DefaultChunkSize: 5,
		MaxChunkSize:     8,
		MaxParts:         4,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return mgr, store, cache, bus
}

func bulkContext(orgID, correlationID, idempotencyKey string) context.Context {
	ctx := security.ContextWithOrganizationID(context.Background(), orgID)
	md := metadata.New()
	md.CorrelationID = correlationID
	md.IdempotencyKey = idempotencyKey
	md.GlobalContext = &metadata.GlobalContext{
		UserID:         "user_1",
		OrganizationID: orgID,
	}
	return metadata.IntoContext(ctx, md)
}

func acceptTestPart(t *testing.T, mgr *Manager, ctx context.Context, transferID string, partNumber int, offset int64, payload string) PartReceipt {
	t.Helper()
	receipt, err := mgr.AcceptPart(ctx, transferID, PartDescriptor{
		PartNumber:        partNumber,
		Offset:            offset,
		Size:              int64(len(payload)),
		ExpectedRawSHA256: shaHex(payload),
	}, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("AcceptPart(%d) error = %v", partNumber, err)
	}
	return receipt
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
}

func shaHex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hasEvent(events []events.Envelope, eventType, correlationID string) bool {
	for _, event := range events {
		if event.EventType == eventType && event.CorrelationID == correlationID {
			return true
		}
	}
	return false
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("reader should not be used")
}

type failingBus struct{}

func (failingBus) Publish(context.Context, events.Envelope) error {
	return errors.New("publish failed")
}

type failAfterFirstBus struct {
	count atomic.Int32
}

func (b *failAfterFirstBus) Publish(context.Context, events.Envelope) error {
	if b.count.Add(1) > 1 {
		return errors.New("publish failed")
	}
	return nil
}

type failingObjectStore struct{}

func (failingObjectStore) PutStream(context.Context, string, io.Reader, int64, objectstore.PutOptions) (objectstore.Object, error) {
	return objectstore.Object{}, errors.New("put failed")
}

func (failingObjectStore) GetRange(context.Context, string, int64, int64) (io.ReadCloser, objectstore.Object, error) {
	return nil, objectstore.Object{}, errors.New("range failed")
}

func (failingObjectStore) Delete(context.Context, string) error {
	return nil
}

type savePlanFailState struct {
	StateStore
}

func (s savePlanFailState) SavePlan(context.Context, TransferPlan) error {
	return errors.New("save plan failed")
}

type saveReceiptFailState struct {
	StateStore
}

func (s saveReceiptFailState) SaveReceipt(context.Context, PartReceipt) error {
	return errors.New("save receipt failed")
}

type saveManifestFailState struct {
	StateStore
}

func (s saveManifestFailState) SaveManifest(context.Context, TransferManifest) error {
	return errors.New("save manifest failed")
}

type listReceiptsFailState struct {
	StateStore
}

func (s listReceiptsFailState) ListReceipts(context.Context, string, string) ([]PartReceipt, error) {
	return nil, errors.New("list receipts failed")
}

type loadManifestFailState struct {
	StateStore
}

func (s loadManifestFailState) LoadManifest(context.Context, string, string) (TransferManifest, error) {
	return TransferManifest{}, errors.New("load manifest failed")
}

type manifestFailStore struct {
	*objectstore.Store
}

func (s manifestFailStore) PutStream(ctx context.Context, key string, reader io.Reader, size int64, opts objectstore.PutOptions) (objectstore.Object, error) {
	if strings.Contains(key, "manifest.json") {
		return objectstore.Object{}, errors.New("manifest put failed")
	}
	return s.Store.PutStream(ctx, key, reader, size, opts)
}

type closeErrorReader struct{}

func (closeErrorReader) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (closeErrorReader) Close() error {
	return errors.New("close failed")
}
func newFSManager(t *testing.T) (*Manager, *objectstore.FSStore) {
	t.Helper()
	store, err := objectstore.NewFSStore(t.TempDir(), "bulk")
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		Cache:            redis.NewMemoryClient("test"),
		EventBus:         events.NewInMemoryBus(50),
		DefaultChunkSize: 4096,
		MaxChunkSize:     1 << 20,
		MaxParts:         8,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return mgr, store
}

func writeTempPart(t *testing.T, payload []byte) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "part.bin")
	if err := os.WriteFile(p, payload, 0o600); err != nil {
		t.Fatalf("write temp part: %v", err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open temp part: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func descFor(payload []byte) PartDescriptor {
	sum := sha256.Sum256(payload)
	return PartDescriptor{PartNumber: 0, Offset: 0, Size: int64(len(payload)), ExpectedRawSHA256: hex.EncodeToString(sum[:])}
}

// TestAcceptDescriptorFilePartZeroCopyLane drives the executable descriptor lane
// end to end through a filesystem object store: the part is hash-verified, moved
// (via copy_file_range where supported), and the stored bytes match the source.
func TestAcceptDescriptorFilePartZeroCopyLane(t *testing.T) {
	mgr, store := newFSManager(t)
	ctx := bulkContext("org_1", "corr_zc", "idem_zc")
	payload := bytes.Repeat([]byte("zero-copy-"), 500) // 5000 bytes

	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:     "zc_1",
		TotalSize:      int64(len(payload)),
		ChunkSize:      int64(len(payload)),
		MaxMemory:      int64(len(payload)),
		Compression:    EncodingIdentity,
		IdempotencyKey: "idem_zc",
		Deadline:       fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}

	src := writeTempPart(t, payload)
	desc := descFor(payload)
	receipt, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, desc, src)
	if err != nil {
		t.Fatalf("AcceptDescriptorFilePart() error = %v", err)
	}

	if receipt.RawSize != int64(len(payload)) {
		t.Fatalf("raw size = %d want %d", receipt.RawSize, len(payload))
	}
	if receipt.RawSHA256 != desc.ExpectedRawSHA256 {
		t.Fatalf("digest = %q want %q", receipt.RawSHA256, desc.ExpectedRawSHA256)
	}
	if receipt.Encoding != EncodingIdentity {
		t.Fatalf("encoding = %q want identity", receipt.Encoding)
	}
	// The zero-copy flag must reflect the platform: true on Linux with
	// copy_file_range, false (streamed fallback) elsewhere.
	if receipt.ZeroCopy != kernellane.ZeroCopyFileSupported() {
		t.Fatalf("ZeroCopy=%v but kernellane support=%v", receipt.ZeroCopy, kernellane.ZeroCopyFileSupported())
	}

	// Read the stored part back from the filesystem object store to confirm the
	// kernel/fallback copy produced byte-identical content.
	reader, _, err := store.Open(ctx, receipt.ObjectKey)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	got, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatalf("read stored part: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("stored bytes differ from source")
	}

	// Re-accepting the same part is an idempotent replay, not a re-upload.
	src2 := writeTempPart(t, payload)
	replay, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, desc, src2)
	if err != nil {
		t.Fatalf("idempotent replay error = %v", err)
	}
	if !replay.IdempotentReplay || replay.RawSHA256 != desc.ExpectedRawSHA256 {
		t.Fatalf("expected idempotent replay, got %+v", replay)
	}
}

func TestAcceptDescriptorFilePartUnknownTransfer(t *testing.T) {
	mgr, _ := newFSManager(t)
	ctx := bulkContext("org_1", "corr_u", "idem_u")
	src := writeTempPart(t, []byte("data"))
	if _, err := mgr.AcceptDescriptorFilePart(ctx, "does-not-exist", descFor([]byte("data")), src); err == nil {
		t.Fatal("unknown transfer should error")
	}
}

// failingFileRangeStore is a FileRangeObjectStore whose zero-copy ingest fails,
// exercising the manager's error-handling and object cleanup on that lane.
type failingFileRangeStore struct {
	*objectstore.FSStore
}

func (f failingFileRangeStore) PutFileRange(context.Context, string, *os.File, int64, objectstore.PutOptions) (objectstore.Object, bool, error) {
	return objectstore.Object{}, false, errAppDependency
}

var errAppDependency = apperrors.New(apperrors.CodeDependency, "injected file-range failure")

// selectiveFailBus fails Publish only for a chosen event type, so Initiate can
// succeed while a later part-accept emit fails.
type selectiveFailBus struct{ failOn string }

func (b selectiveFailBus) Publish(_ context.Context, env events.Envelope) error {
	if env.EventType == b.failOn {
		return errAppDependency
	}
	return nil
}

func TestAcceptDescriptorFilePartSurfacesEventFailure(t *testing.T) {
	store, err := objectstore.NewFSStore(t.TempDir(), "bucket")
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		Cache:            redis.NewMemoryClient("test"),
		EventBus:         selectiveFailBus{failOn: "bulk:part:accept:v1:requested"},
		DefaultChunkSize: 4096,
		MaxChunkSize:     1 << 20,
		MaxParts:         8,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_ef", "idem_ef")
	payload := []byte("event-failure-bytes")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "ef_1", TotalSize: int64(len(payload)), ChunkSize: int64(len(payload)),
		MaxMemory: int64(len(payload)), Compression: EncodingIdentity, IdempotencyKey: "idem_ef",
		Deadline: fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}
	src := writeTempPart(t, payload)
	if _, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, descFor(payload), src); err == nil {
		t.Fatal("event publish failure should surface as an error")
	}
}

func TestAcceptDescriptorFilePartSurfacesStoreFailure(t *testing.T) {
	base, err := objectstore.NewFSStore(t.TempDir(), "bucket")
	if err != nil {
		t.Fatalf("NewFSStore() error = %v", err)
	}
	mgr, err := NewManager(Options{
		ObjectStore:      failingFileRangeStore{base},
		Cache:            redis.NewMemoryClient("test"),
		EventBus:         events.NewInMemoryBus(50),
		DefaultChunkSize: 4096,
		MaxChunkSize:     1 << 20,
		MaxParts:         8,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	ctx := bulkContext("org_1", "corr_sf", "idem_sf")
	payload := []byte("store-failure-bytes")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "sf_1", TotalSize: int64(len(payload)), ChunkSize: int64(len(payload)),
		MaxMemory: int64(len(payload)), Compression: EncodingIdentity, IdempotencyKey: "idem_sf",
		Deadline: fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}
	src := writeTempPart(t, payload)
	if _, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, descFor(payload), src); err == nil {
		t.Fatal("store failure should surface as an error")
	}
}

// TestAcceptDescriptorFilePartFallsBackToStreaming proves graceful degradation:
// an object store without FileRange support uses the streaming lane, never
// reports zero-copy, and still verifies integrity.
func TestAcceptDescriptorFilePartFallsBackToStreaming(t *testing.T) {
	mgr, _, _, _ := newTestManager(t) // in-memory store: not a FileRangeObjectStore
	ctx := bulkContext("org_1", "corr_fb", "idem_fb")
	payload := []byte("fbbytes") // within newTestManager's small chunk bounds

	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:     "fb_1",
		TotalSize:      int64(len(payload)),
		ChunkSize:      int64(len(payload)),
		MaxMemory:      int64(len(payload)),
		Compression:    EncodingIdentity,
		IdempotencyKey: "idem_fb",
		Deadline:       fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}

	src := writeTempPart(t, payload)
	receipt, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, descFor(payload), src)
	if err != nil {
		t.Fatalf("AcceptDescriptorFilePart() error = %v", err)
	}
	if receipt.ZeroCopy {
		t.Fatal("in-memory store must not report zero-copy")
	}
	if receipt.RawSHA256 != descFor(payload).ExpectedRawSHA256 {
		t.Fatal("digest mismatch on fallback lane")
	}
}

// TestAcceptDescriptorFilePartCompressionUsesStreamingLane ensures a compressed
// transfer never takes the zero-copy lane (copy_file_range cannot compress).
func TestAcceptDescriptorFilePartCompressionUsesStreamingLane(t *testing.T) {
	mgr, _ := newFSManager(t)
	ctx := bulkContext("org_1", "corr_gz", "idem_gz")
	payload := bytes.Repeat([]byte("compressible "), 64)

	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:     "gz_1",
		TotalSize:      int64(len(payload)),
		ChunkSize:      int64(len(payload)),
		MaxMemory:      int64(len(payload)),
		Compression:    EncodingGzip,
		IdempotencyKey: "idem_gz",
		Deadline:       fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}

	src := writeTempPart(t, payload)
	receipt, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, descFor(payload), src)
	if err != nil {
		t.Fatalf("AcceptDescriptorFilePart() error = %v", err)
	}
	if receipt.ZeroCopy {
		t.Fatal("compressed transfer must not use zero-copy lane")
	}
	if receipt.Encoding != EncodingGzip {
		t.Fatalf("encoding = %q want gzip", receipt.Encoding)
	}
}

func TestAcceptDescriptorFilePartValidatesInputs(t *testing.T) {
	mgr, _ := newFSManager(t)
	ctx := bulkContext("org_1", "corr_v", "idem_v")
	plan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID: "v_1", TotalSize: 4, ChunkSize: 4, MaxMemory: 4,
		Compression: EncodingIdentity, IdempotencyKey: "idem_v", Deadline: fixedNow().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Initiate() error = %v", err)
	}

	if _, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, descFor([]byte("data")), nil); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("nil source error = %v want validation", err)
	}

	// Wrong declared digest must be rejected before any object is stored.
	src := writeTempPart(t, []byte("data"))
	bad := PartDescriptor{PartNumber: 0, Size: 4, ExpectedRawSHA256: shaHex("nope")}
	if _, err := mgr.AcceptDescriptorFilePart(ctx, plan.TransferID, bad, src); err == nil {
		t.Fatal("digest mismatch should error")
	}
}
