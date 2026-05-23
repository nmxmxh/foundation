package bulk

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
	transport "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/transport"
	apperrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/objectstore"
)

func TestPipelineHandlesControlPlaneAndStreamsPartReaders(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_1", "idem_pipeline_1")

	initEnv := transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id":     "pipe_1",
		"total_size":      int64(10),
		"chunk_size":      int64(5),
		"max_memory":      int64(5),
		"content_type":    "text/plain",
		"compression":     EncodingIdentity,
		"idempotency_key": "idem_pipeline_1",
		"deadline":        fixedNow().Add(time.Minute).Format(time.RFC3339Nano),
		"attributes": map[string]any{
			"domain": "dataset",
		},
	}, nil)
	planEnv, err := pipeline.HandleControl(ctx, initEnv)
	if err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	if planEnv.EventType != EventInitiateSuccess || planEnv.Payload["transfer_id"] != "pipe_1" {
		t.Fatalf("plan envelope = %+v", planEnv)
	}
	lane, ok := planEnv.Payload["lane"].(map[string]any)
	if !ok || lane["copy_budget"] != "bounded-part-reader" || lane["zero_copy_available"] != true {
		t.Fatalf("lane diagnostics = %#v", planEnv.Payload["lane"])
	}

	first := acceptPipelinePart(t, pipeline, ctx, "pipe_1", 0, 0, "hello")
	if first.Payload["raw_size"] != int64(5) {
		t.Fatalf("first receipt envelope = %+v", first)
	}

	statusEnv, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventStatusRequested, map[string]any{
		"transfer_id": "pipe_1",
	}, nil))
	if err != nil {
		t.Fatalf("HandleControl(status) error = %v", err)
	}
	if statusEnv.Payload["bytes_accepted"] != int64(5) || statusEnv.Payload["parts_accepted"] != 1 {
		t.Fatalf("partial status = %+v", statusEnv.Payload)
	}
	missing := statusEnv.Payload["missing_parts"].([]map[string]any)
	if len(missing) != 1 || missing[0]["part_number"] != 1 || missing[0]["offset"] != int64(5) {
		t.Fatalf("missing parts = %+v", missing)
	}

	second := acceptPipelinePart(t, pipeline, ctx, "pipe_1", 1, 5, "world")
	root := manifestRoot([]PartReceipt{
		receiptFromEnvelope(first),
		receiptFromEnvelope(second),
	})
	completeEnv, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventCompleteRequested, map[string]any{
		"transfer_id":          "pipe_1",
		"expected_root_sha256": root,
	}, nil))
	if err != nil {
		t.Fatalf("HandleControl(complete) error = %v", err)
	}
	if completeEnv.EventType != EventCompleteSuccess || completeEnv.Payload["root_sha256"] != root {
		t.Fatalf("complete envelope = %+v", completeEnv)
	}

	reader, _, err := mgr.OpenRange(ctx, "pipe_1", 0, 10)
	if err != nil {
		t.Fatalf("OpenRange() error = %v", err)
	}
	payload, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(payload) != "helloworld" {
		t.Fatalf("range payload = %q err=%v", string(payload), err)
	}
}

func TestPipelineConstructionDefaultsAndValidation(t *testing.T) {
	if _, err := NewPipeline(PipelineOptions{}); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("NewPipeline(nil manager) error = %v", err)
	}
	mgr, _, _, _ := newTestManager(t)
	pipeline, err := NewPipeline(PipelineOptions{Manager: mgr})
	if err != nil {
		t.Fatalf("NewPipeline(defaults) error = %v", err)
	}
	diag := pipeline.Diagnostics(TransferPlan{ChunkSize: 1, MaxMemory: 1, Compression: EncodingIdentity})
	if diag.Ingress != "app-owned" || diag.ObjectStoreBackend != "unknown" {
		t.Fatalf("default diagnostics = %+v", diag)
	}
}

func TestPipelineAcceptHTTPPartUsesStreamReader(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_http", "idem_pipeline_http")
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "http_part",
		"total_size":  int64(4),
		"chunk_size":  int64(4),
		"max_memory":  int64(4),
	}, nil)); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	req := HTTPPartRequest{
		Envelope: transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
			"transfer_id":         "http_part",
			"part_number":         0,
			"offset":              int64(0),
			"size":                int64(4),
			"expected_raw_sha256": shaHex("body"),
		}, nil),
		Reader: strings.NewReader("body"),
	}
	out, err := pipeline.AcceptHTTPPart(ctx, req)
	if err != nil {
		t.Fatalf("AcceptHTTPPart() error = %v", err)
	}
	if out.EventType != EventPartAcceptSuccess || out.Payload["raw_sha256"] != shaHex("body") {
		t.Fatalf("AcceptHTTPPart() envelope = %+v", out)
	}
}

func TestPipelineAcceptDescriptorPartUsesSameHostSource(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_descriptor", "idem_pipeline_descriptor")
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "descriptor_part",
		"total_size":  int64(4),
		"chunk_size":  int64(4),
		"max_memory":  int64(4),
	}, nil)); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	env := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
		"transfer_id":         "descriptor_part",
		"part_number":         0,
		"offset":              int64(0),
		"size":                int64(4),
		"expected_raw_sha256": shaHex("shm!"),
	}, nil)
	out, err := pipeline.AcceptDescriptorPart(ctx, env, descriptorSource{payload: "shm!"})
	if err != nil {
		t.Fatalf("AcceptDescriptorPart() error = %v", err)
	}
	if out.EventType != EventPartAcceptSuccess || out.Payload["raw_sha256"] != shaHex("shm!") {
		t.Fatalf("AcceptDescriptorPart() envelope = %+v", out)
	}
}

func TestPipelineAcceptDescriptorPartFailures(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_descriptor_fail", "idem_pipeline_descriptor_fail")
	env := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
		"transfer_id":         "descriptor_missing",
		"part_number":         0,
		"offset":              int64(0),
		"size":                int64(4),
		"expected_raw_sha256": shaHex("shm!"),
	}, nil)
	if _, err := pipeline.AcceptDescriptorPart(ctx, env, nil); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("nil source error = %v", err)
	}
	badEvent := env
	badEvent.EventType = EventStatusRequested
	if _, err := pipeline.AcceptDescriptorPart(ctx, badEvent, descriptorSource{payload: "shm!"}); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("bad event error = %v", err)
	}
	if _, err := pipeline.AcceptDescriptorPart(ctx, env, failingDescriptorSource{}); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("failing source error = %v", err)
	}
}

func TestPipelineSignedObjectStorePartBypassesAppByteProxy(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://bulk-signed-tests",
		Bucket:   "bulk",
	})
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		DefaultChunkSize: 5,
		MaxChunkSize:     8,
		MaxParts:         4,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_signed", "idem_pipeline_signed")
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "signed_1",
		"total_size":  int64(5),
		"chunk_size":  int64(5),
		"max_memory":  int64(5),
	}, nil)); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	desc := PartDescriptor{
		PartNumber:        0,
		Offset:            0,
		Size:              5,
		ExpectedRawSHA256: shaHex("cloud"),
		ContentType:       "text/plain",
	}
	request := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{"transfer_id": "signed_1"}, nil)
	grantEnv, err := pipeline.GrantSignedPart(ctx, request, SignedPartRequest{
		TransferID: "signed_1",
		Descriptor: desc,
		Expiry:     time.Minute,
	})
	if err != nil {
		t.Fatalf("GrantSignedPart() error = %v", err)
	}
	objectKey := grantEnv.Payload["object_key"].(string)
	if objectKey == "" || grantEnv.Payload["upload_url"] == "" {
		t.Fatalf("grant envelope = %+v", grantEnv)
	}
	if _, err := store.PutStream(ctx, objectKey, strings.NewReader("cloud"), 5, objectstore.PutOptions{ContentType: "text/plain"}); err != nil {
		t.Fatalf("direct object upload error = %v", err)
	}
	receiptEnv, err := pipeline.AcceptSignedPart(ctx, request, "signed_1", desc)
	if err != nil {
		t.Fatalf("AcceptSignedPart() error = %v", err)
	}
	if receiptEnv.Payload["raw_sha256"] != shaHex("cloud") {
		t.Fatalf("receipt envelope = %+v", receiptEnv)
	}
	replayed, err := pipeline.AcceptSignedPart(ctx, request, "signed_1", desc)
	if err != nil {
		t.Fatalf("AcceptSignedPart(replay) error = %v", err)
	}
	if replayed.Payload["raw_sha256"] != shaHex("cloud") {
		t.Fatalf("replayed receipt envelope = %+v", replayed)
	}
}

func TestPipelineSignedObjectStorePartRejectsTamperedObject(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://bulk-signed-tamper-tests",
		Bucket:   "bulk",
	})
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		DefaultChunkSize: 5,
		MaxChunkSize:     8,
		MaxParts:         4,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_tamper", "idem_pipeline_tamper")
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "signed_tamper",
		"total_size":  int64(5),
		"chunk_size":  int64(5),
		"max_memory":  int64(5),
	}, nil)); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	desc := PartDescriptor{
		PartNumber:        0,
		Offset:            0,
		Size:              5,
		ExpectedRawSHA256: shaHex("right"),
	}
	request := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{"transfer_id": "signed_tamper"}, nil)
	grantEnv, err := pipeline.GrantSignedPart(ctx, request, SignedPartRequest{
		TransferID: "signed_tamper",
		Descriptor: desc,
		Expiry:     time.Minute,
	})
	if err != nil {
		t.Fatalf("GrantSignedPart() error = %v", err)
	}
	if _, err := store.PutStream(ctx, grantEnv.Payload["object_key"].(string), strings.NewReader("wrong"), 5, objectstore.PutOptions{}); err != nil {
		t.Fatalf("direct object upload error = %v", err)
	}
	if _, err := pipeline.AcceptSignedPart(ctx, request, "signed_tamper", desc); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("AcceptSignedPart(tampered) error = %v", err)
	}
	status, err := mgr.Status(ctx, "signed_tamper")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.PartsAccepted != 0 || len(status.MissingParts) != 1 {
		t.Fatalf("status after tamper = %+v", status)
	}
}

func TestPipelineSignedObjectStorePartRejectsOversizedDirectObject(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://bulk-signed-oversize-tests",
		Bucket:   "bulk",
	})
	mgr, err := NewManager(Options{
		ObjectStore:      store,
		DefaultChunkSize: 5,
		MaxChunkSize:     8,
		MaxParts:         4,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_oversize", "idem_pipeline_oversize")
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "signed_oversize",
		"total_size":  int64(5),
		"chunk_size":  int64(5),
		"max_memory":  int64(5),
	}, nil)); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	desc := PartDescriptor{
		PartNumber:        0,
		Offset:            0,
		Size:              5,
		ExpectedRawSHA256: shaHex("right"),
	}
	request := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{"transfer_id": "signed_oversize"}, nil)
	grantEnv, err := pipeline.GrantSignedPart(ctx, request, SignedPartRequest{
		TransferID: "signed_oversize",
		Descriptor: desc,
		Expiry:     time.Minute,
	})
	if err != nil {
		t.Fatalf("GrantSignedPart() error = %v", err)
	}
	if _, err := store.PutStream(ctx, grantEnv.Payload["object_key"].(string), strings.NewReader("right!"), 6, objectstore.PutOptions{}); err != nil {
		t.Fatalf("direct object upload error = %v", err)
	}
	if _, err := pipeline.AcceptSignedPart(ctx, request, "signed_oversize", desc); !apperrors.Is(err, apperrors.CodeQuotaExceeded) {
		t.Fatalf("AcceptSignedPart(oversized) error = %v", err)
	}
}

func TestPipelineSignedObjectStoreFailureBranches(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_signed_fail", "idem_pipeline_signed_fail")
	desc := PartDescriptor{PartNumber: 0, Offset: 0, Size: 1, ExpectedRawSHA256: shaHex("x")}
	if _, err := pipeline.GrantSignedPart(ctx, transport.CreateEnvelope(EventPartAcceptRequest, nil, nil), SignedPartRequest{
		TransferID: "missing",
		Descriptor: desc,
	}); !apperrors.Is(err, apperrors.CodeNotFound) {
		t.Fatalf("missing grant error = %v", err)
	}

	store := objectstore.New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://bulk-no-presign", Bucket: "bulk"})
	noPresignMgr, err := NewManager(Options{
		ObjectStore: noPresignStore{ObjectStore: store},
		Clock:       fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager(noPresign) error = %v", err)
	}
	noPresignPipe := newTestPipeline(t, noPresignMgr)
	if _, err := noPresignMgr.Initiate(ctx, InitiateRequest{TransferID: "no_presign", TotalSize: 1, ChunkSize: 1, MaxMemory: 1}); err != nil {
		t.Fatalf("Initiate(no_presign) error = %v", err)
	}
	if _, err := noPresignPipe.GrantSignedPart(ctx, transport.CreateEnvelope(EventPartAcceptRequest, nil, nil), SignedPartRequest{
		TransferID: "no_presign",
		Descriptor: desc,
	}); !apperrors.Is(err, apperrors.CodeNotImplemented) {
		t.Fatalf("no presign error = %v", err)
	}

	presignFailMgr, err := NewManager(Options{
		ObjectStore: presignFailStore{ObjectStore: store},
		Clock:       fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager(presignFail) error = %v", err)
	}
	presignFailPipe := newTestPipeline(t, presignFailMgr)
	if _, err := presignFailMgr.Initiate(ctx, InitiateRequest{TransferID: "presign_fail", TotalSize: 1, ChunkSize: 1, MaxMemory: 1}); err != nil {
		t.Fatalf("Initiate(presign_fail) error = %v", err)
	}
	if _, err := presignFailPipe.GrantSignedPart(ctx, transport.CreateEnvelope(EventPartAcceptRequest, nil, nil), SignedPartRequest{
		TransferID: "presign_fail",
		Descriptor: desc,
		Expiry:     30 * time.Second,
	}); !apperrors.Is(err, apperrors.CodeDependency) {
		t.Fatalf("presign failure error = %v", err)
	}

	compressedPlan, err := mgr.Initiate(ctx, InitiateRequest{
		TransferID:  "signed_compressed",
		TotalSize:   1,
		ChunkSize:   1,
		MaxMemory:   1,
		Compression: EncodingGzip,
	})
	if err != nil {
		t.Fatalf("Initiate(signed_compressed) error = %v", err)
	}
	if _, err := pipeline.AcceptSignedPart(ctx, transport.CreateEnvelope(EventPartAcceptRequest, nil, nil), compressedPlan.TransferID, desc); !apperrors.Is(err, apperrors.CodeNotImplemented) {
		t.Fatalf("compressed signed accept error = %v", err)
	}
	if _, err := pipeline.AcceptSignedPart(ctx, transport.CreateEnvelope(EventPartAcceptRequest, nil, nil), "missing_signed", desc); !apperrors.Is(err, apperrors.CodeNotFound) {
		t.Fatalf("missing signed accept error = %v", err)
	}
}

func TestPipelineRejectsBulkBytesInControlEnvelope(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_2", "idem_pipeline_2")

	_, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
		"transfer_id":         "pipe_bad",
		"part_number":         0,
		"offset":              0,
		"size":                5,
		"expected_raw_sha256": shaHex("hello"),
		"payload":             "hello",
	}, nil))
	if !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("HandleControl(part bytes) error = %v", err)
	}
}

func TestPipelineReportsFailuresAsTypedEnvelopes(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_3", "idem_pipeline_3")

	env, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "missing_size",
	}, nil))
	if !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("HandleControl(invalid) error = %v", err)
	}
	if env.EventType != EventFailed || env.Payload["request_event"] != EventInitiateRequested {
		t.Fatalf("failure envelope = %+v", env)
	}
}

func TestPipelinePreservesRuntimeTransportMetadata(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_8", "idem_pipeline_8")
	request := transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "metadata_1",
		"total_size":  int64(1),
		"chunk_size":  int64(1),
		"max_memory":  int64(1),
	}, nil)
	request.Metadata.CorrelationID = "corr_request_metadata"
	request.Metadata.RequestID = "req_request_metadata"
	request.Metadata.IdempotencyKey = "idem_request_metadata"

	response, err := pipeline.HandleControl(ctx, request)
	if err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	if response.Metadata.CorrelationID != request.Metadata.CorrelationID ||
		response.Metadata.RequestID != request.Metadata.RequestID ||
		response.Metadata.IdempotencyKey != request.Metadata.IdempotencyKey {
		t.Fatalf("response metadata = %+v request=%+v", response.Metadata, request.Metadata)
	}

	failureRequest := request
	failureRequest.EventType = EventStatusRequested
	failureRequest.Payload = map[string]any{"transfer_id": "missing"}
	failure, err := pipeline.HandleControl(ctx, failureRequest)
	if !apperrors.Is(err, apperrors.CodeNotFound) {
		t.Fatalf("HandleControl(missing status) error = %v", err)
	}
	if failure.Metadata.CorrelationID != request.Metadata.CorrelationID ||
		failure.Metadata.IdempotencyKey != request.Metadata.IdempotencyKey {
		t.Fatalf("failure metadata = %+v request=%+v", failure.Metadata, request.Metadata)
	}
}

func TestPipelineResumeSurvivesManagerHandoffAndReplaysReceipts(t *testing.T) {
	store := objectstore.New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://bulk-pipeline-tests",
		Bucket:   "bulk",
	})
	state := NewMemoryStateStore()
	firstManager, err := NewManager(Options{
		ObjectStore:      store,
		StateStore:       state,
		DefaultChunkSize: 4,
		MaxChunkSize:     8,
		MaxParts:         4,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager(first) error = %v", err)
	}
	secondManager, err := NewManager(Options{
		ObjectStore:      store,
		StateStore:       state,
		DefaultChunkSize: 4,
		MaxChunkSize:     8,
		MaxParts:         4,
		Clock:            fixedNow,
	})
	if err != nil {
		t.Fatalf("NewManager(second) error = %v", err)
	}
	firstPipeline := newTestPipeline(t, firstManager)
	secondPipeline := newTestPipeline(t, secondManager)
	ctx := bulkContext("org_1", "corr_pipeline_4", "idem_pipeline_4")

	if _, err := firstPipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "handoff_1",
		"total_size":  int64(8),
		"chunk_size":  int64(4),
		"max_memory":  int64(4),
	}, nil)); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	firstReceipt := acceptPipelinePart(t, firstPipeline, ctx, "handoff_1", 0, 0, "left")
	replayed, err := secondPipeline.AcceptPartEnvelope(ctx, transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
		"transfer_id":         "handoff_1",
		"part_number":         0,
		"offset":              int64(0),
		"size":                int64(4),
		"expected_raw_sha256": shaHex("left"),
	}, nil), errReader{})
	if err != nil {
		t.Fatalf("AcceptPartEnvelope(replay) error = %v", err)
	}
	if replayed.Payload["raw_sha256"] != firstReceipt.Payload["raw_sha256"] {
		t.Fatalf("replayed receipt = %+v first=%+v", replayed.Payload, firstReceipt.Payload)
	}

	statusEnv, err := secondPipeline.HandleControl(ctx, transport.CreateEnvelope(EventStatusRequested, map[string]any{
		"transfer_id": "handoff_1",
	}, nil))
	if err != nil {
		t.Fatalf("HandleControl(status) error = %v", err)
	}
	if statusEnv.Payload["parts_accepted"] != 1 {
		t.Fatalf("handoff status = %+v", statusEnv.Payload)
	}
	if token := statusEnv.Payload["resume_token"].(string); !strings.HasPrefix(token, "bulk1_") {
		t.Fatalf("resume token = %q", token)
	}
}

func TestPipelinePreservesTenantIsolation(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	owner := bulkContext("org_1", "corr_pipeline_5", "idem_pipeline_5")
	other := bulkContext("org_2", "corr_pipeline_6", "idem_pipeline_6")

	if _, err := pipeline.HandleControl(owner, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "tenant_1",
		"total_size":  int64(5),
		"chunk_size":  int64(5),
		"max_memory":  int64(5),
	}, nil)); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	env, err := pipeline.HandleControl(other, transport.CreateEnvelope(EventStatusRequested, map[string]any{
		"transfer_id": "tenant_1",
	}, nil))
	if !apperrors.Is(err, apperrors.CodeNotFound) {
		t.Fatalf("cross-tenant status error = %v env=%+v", err, env)
	}
	if env.EventType != EventFailed {
		t.Fatalf("cross-tenant envelope = %+v", env)
	}
}

func TestPipelineDecodeBoundaries(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_7", "idem_pipeline_7")

	cases := []struct {
		name    string
		event   string
		payload map[string]any
	}{
		{
			name:  "fractional total size",
			event: EventInitiateRequested,
			payload: map[string]any{
				"transfer_id": "bad_fraction",
				"total_size":  1.5,
			},
		},
		{
			name:  "bad deadline",
			event: EventInitiateRequested,
			payload: map[string]any{
				"transfer_id": "bad_deadline",
				"total_size":  int64(1),
				"deadline":    "tomorrow",
			},
		},
		{
			name:  "missing part hash",
			event: EventPartAcceptRequest,
			payload: map[string]any{
				"transfer_id": "bad_part",
				"part_number": 0,
				"offset":      0,
				"size":        1,
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			env := transport.CreateEnvelope(tt.event, tt.payload, nil)
			var err error
			if tt.event == EventPartAcceptRequest {
				_, err = pipeline.AcceptPartEnvelope(ctx, env, strings.NewReader("x"))
			} else {
				_, err = pipeline.HandleControl(ctx, env)
			}
			if !apperrors.Is(err, apperrors.CodeValidation) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPipelineDecodeNumericAndEventBoundaries(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_decode", "idem_pipeline_decode")
	initEnv := transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id":     "decode_ok",
		"total_size":      "2",
		"chunk_size":      int32(1),
		"max_memory":      1,
		"max_parts":       "2",
		"attributes":      map[string]string{"source": "decode"},
		"idempotency_key": staticStringer("idem_stringer"),
	}, nil)
	if _, err := pipeline.HandleControl(ctx, initEnv); err != nil {
		t.Fatalf("HandleControl(decode_ok) error = %v", err)
	}
	if _, err := pipeline.AcceptPartEnvelope(ctx, transport.CreateEnvelope(EventStatusRequested, nil, nil), strings.NewReader("x")); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("unsupported part event error = %v", err)
	}
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventCompleteRequested, map[string]any{}, nil)); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("missing complete transfer error = %v", err)
	}
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "bad_bool_number",
		"total_size":  true,
	}, nil)); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("unsupported numeric value error = %v", err)
	}
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "bad_chunk",
		"total_size":  int64(1),
		"chunk_size":  true,
	}, nil)); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("bad chunk size error = %v", err)
	}
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "bad_memory",
		"total_size":  int64(1),
		"max_memory":  true,
	}, nil)); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("bad max memory error = %v", err)
	}
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "bad_parts",
		"total_size":  int64(1),
		"max_parts":   true,
	}, nil)); !apperrors.Is(err, apperrors.CodeValidation) {
		t.Fatalf("bad max parts error = %v", err)
	}
	for name, payload := range map[string]map[string]any{
		"part_number": {"transfer_id": "decode_ok", "part_number": true, "offset": int64(0), "size": int64(1), "expected_raw_sha256": shaHex("x")},
		"offset":      {"transfer_id": "decode_ok", "part_number": 0, "offset": true, "size": int64(1), "expected_raw_sha256": shaHex("x")},
		"size":        {"transfer_id": "decode_ok", "part_number": 0, "offset": int64(0), "size": true, "expected_raw_sha256": shaHex("x")},
	} {
		t.Run("bad "+name, func(t *testing.T) {
			if _, err := pipeline.AcceptPartEnvelope(ctx, transport.CreateEnvelope(EventPartAcceptRequest, payload, nil), strings.NewReader("x")); !apperrors.Is(err, apperrors.CodeValidation) {
				t.Fatalf("bad %s error = %v", name, err)
			}
		})
	}
}

func TestPipelineRejectsOversizedReaderAndConflictingReplay(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_9", "idem_pipeline_9")

	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "reader_bounds",
		"total_size":  int64(2),
		"chunk_size":  int64(2),
		"max_memory":  int64(2),
	}, nil)); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	env := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
		"transfer_id":         "reader_bounds",
		"part_number":         0,
		"offset":              int64(0),
		"size":                int64(2),
		"expected_raw_sha256": shaHex("ab"),
	}, nil)
	if _, err := pipeline.AcceptPartEnvelope(ctx, env, strings.NewReader("abc")); !apperrors.Is(err, apperrors.CodeQuotaExceeded) {
		t.Fatalf("oversized AcceptPartEnvelope() error = %v", err)
	}
	if _, err := pipeline.AcceptPartEnvelope(ctx, env, strings.NewReader("ab")); err != nil {
		t.Fatalf("AcceptPartEnvelope(valid) error = %v", err)
	}
	conflict := env
	conflict.Payload = map[string]any{
		"transfer_id":         "reader_bounds",
		"part_number":         0,
		"offset":              int64(0),
		"size":                int64(2),
		"expected_raw_sha256": shaHex("zz"),
	}
	if _, err := pipeline.AcceptPartEnvelope(ctx, conflict, errReader{}); !apperrors.Is(err, apperrors.CodeConflict) {
		t.Fatalf("conflicting replay error = %v", err)
	}
}

func TestPipelineConcurrentDuplicateAcceptIsIdempotent(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	ctx := bulkContext("org_1", "corr_pipeline_10", "idem_pipeline_10")
	if _, err := pipeline.HandleControl(ctx, transport.CreateEnvelope(EventInitiateRequested, map[string]any{
		"transfer_id": "concurrent_1",
		"total_size":  int64(4),
		"chunk_size":  int64(4),
		"max_memory":  int64(4),
	}, nil)); err != nil {
		t.Fatalf("HandleControl(initiate) error = %v", err)
	}
	env := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
		"transfer_id":         "concurrent_1",
		"part_number":         0,
		"offset":              int64(0),
		"size":                int64(4),
		"expected_raw_sha256": shaHex("same"),
	}, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := pipeline.AcceptPartEnvelope(ctx, env, strings.NewReader("same"))
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent AcceptPartEnvelope() error = %v", err)
		}
	}
	status, err := mgr.Status(ctx, "concurrent_1")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.PartsAccepted != 1 || status.BytesAccepted != 4 {
		t.Fatalf("status = %+v", status)
	}
}

func TestPipelineDiagnosticsUseInjectedClock(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	plan := TransferPlan{
		TransferID:  "diag_1",
		ChunkSize:   5,
		MaxMemory:   5,
		Compression: EncodingIdentity,
		Deadline:    fixedNow().Add(30 * time.Second),
	}
	diag := pipeline.Diagnostics(plan)
	if diag.DeadlineRisk != "high" {
		t.Fatalf("deadline risk = %q", diag.DeadlineRisk)
	}
	plan.Deadline = fixedNow().Add(-time.Second)
	diag = pipeline.Diagnostics(plan)
	if diag.DeadlineRisk != "expired" {
		t.Fatalf("deadline risk = %q", diag.DeadlineRisk)
	}
	plan.Deadline = fixedNow().Add(2 * time.Minute)
	diag = pipeline.Diagnostics(plan)
	if diag.DeadlineRisk != "medium" {
		t.Fatalf("deadline risk = %q", diag.DeadlineRisk)
	}
	plan.Deadline = fixedNow().Add(10 * time.Minute)
	diag = pipeline.Diagnostics(plan)
	if diag.DeadlineRisk != "low" {
		t.Fatalf("deadline risk = %q", diag.DeadlineRisk)
	}
}

func TestDetectPlatformCapabilitiesAreConservative(t *testing.T) {
	caps := DetectPlatformCapabilities()
	if caps.OS == "" {
		t.Fatalf("capabilities missing OS: %+v", caps)
	}
	opts := caps.PipelineOptions()
	if caps.ZeroCopyAvailable != opts.ZeroCopyAvailable ||
		caps.MPTCPAvailable != opts.MPTCPAvailable ||
		caps.KernelPacing != opts.KernelPacing {
		t.Fatalf("options do not mirror capabilities: caps=%+v opts=%+v", caps, opts)
	}

	darwin := detectPlatformCapabilities("darwin", func() bool { return true })
	if darwin.ZeroCopyAvailable || darwin.MPTCPAvailable || darwin.Notes["fallback"] == "" {
		t.Fatalf("darwin capabilities = %+v", darwin)
	}
	linuxEnabled := detectPlatformCapabilities("linux", func() bool { return true })
	if !linuxEnabled.ZeroCopyAvailable || !linuxEnabled.KernelPacing || !linuxEnabled.MPTCPAvailable {
		t.Fatalf("linux enabled capabilities = %+v", linuxEnabled)
	}
	linuxDisabled := detectPlatformCapabilities("linux", func() bool { return false })
	if linuxDisabled.MPTCPAvailable || linuxDisabled.Notes["mptcp"] == "" || linuxDisabled.Notes["quic"] == "" {
		t.Fatalf("linux disabled capabilities = %+v", linuxDisabled)
	}
	for _, payload := range []string{"1\n", " y ", "enabled"} {
		if !linuxMPTCPEnabledWith(func(string) ([]byte, error) { return []byte(payload), nil }) {
			t.Fatalf("mptcp payload %q was not enabled", payload)
		}
	}
	if linuxMPTCPEnabledWith(func(string) ([]byte, error) { return nil, errors.New("missing") }) ||
		linuxMPTCPEnabledWith(func(string) ([]byte, error) { return []byte("0"), nil }) {
		t.Fatal("mptcp disabled/error payload reported enabled")
	}
	_ = linuxMPTCPEnabled()
}

func TestPipelinePlansAdaptiveLanes(t *testing.T) {
	mgr, _, _, _ := newTestManager(t)
	pipeline := newTestPipeline(t, mgr)
	base := TransferPlan{
		TransferID:  "lane_1",
		ChunkSize:   5,
		MaxMemory:   5,
		Compression: EncodingIdentity,
	}
	part := PartDescriptor{PartNumber: 0, Size: 5}

	descriptor := pipeline.PlanLane(LaneRequest{
		Plan:                base,
		Part:                part,
		Locality:            "same-host",
		TrustedProducer:     true,
		DescriptorAvailable: true,
		HTTPStreamAvailable: true,
	})
	if descriptor.Selected != LaneDescriptor {
		t.Fatalf("descriptor lane = %+v", descriptor)
	}
	untrustedDescriptor := pipeline.PlanLane(LaneRequest{
		Plan:                base,
		Part:                part,
		Locality:            "same-host",
		DescriptorAvailable: true,
		HTTPStreamAvailable: true,
	})
	if untrustedDescriptor.Selected != LaneHTTPStream {
		t.Fatalf("untrusted descriptor lane = %+v", untrustedDescriptor)
	}

	signed := pipeline.PlanLane(LaneRequest{
		Plan:                base,
		Part:                part,
		Locality:            "internet",
		DirectObjectStore:   true,
		HTTPStreamAvailable: true,
	})
	if signed.Selected != LaneSignedObjectStore {
		t.Fatalf("signed lane = %+v", signed)
	}

	kernel := pipeline.PlanLane(LaneRequest{
		Plan:                 base,
		Part:                 part,
		Locality:             "internet",
		HTTPStreamAvailable:  true,
		KernelAdapterEnabled: true,
		Capabilities: PlatformCapabilities{
			OS:                "linux",
			ZeroCopyAvailable: true,
			KernelPacing:      true,
		},
	})
	if kernel.Selected != LaneKernelZeroCopy {
		t.Fatalf("kernel lane = %+v", kernel)
	}
	kernelNoPacing := pipeline.PlanLane(LaneRequest{
		Plan:                 base,
		Part:                 part,
		Locality:             "internet",
		HTTPStreamAvailable:  true,
		KernelAdapterEnabled: true,
		Capabilities: PlatformCapabilities{
			OS:                "linux",
			ZeroCopyAvailable: true,
		},
	})
	if kernelNoPacing.Selected != LaneKernelZeroCopy {
		t.Fatalf("kernel without pacing lane = %+v", kernelNoPacing)
	}

	quic := pipeline.PlanLane(LaneRequest{
		Plan:                 base,
		Part:                 part,
		Locality:             "internet",
		HTTPStreamAvailable:  true,
		QUICAdapterAvailable: true,
	})
	if quic.Selected != LaneQUIC {
		t.Fatalf("quic lane = %+v", quic)
	}

	mptcp := pipeline.PlanLane(LaneRequest{
		Plan:                 base,
		Part:                 part,
		Locality:             "internet",
		HTTPStreamAvailable:  true,
		KernelAdapterEnabled: true,
		Capabilities: PlatformCapabilities{
			OS:             "linux",
			MPTCPAvailable: true,
			KernelPacing:   true,
		},
	})
	if mptcp.Selected != LaneMPTCP {
		t.Fatalf("mptcp lane = %+v", mptcp)
	}

	fallback := pipeline.PlanLane(LaneRequest{
		Plan:                base,
		Part:                part,
		Locality:            "internet",
		HTTPStreamAvailable: true,
	})
	if fallback.Selected != LaneHTTPStream || fallback.Diagnostic.Fallback == "" {
		t.Fatalf("fallback lane = %+v", fallback)
	}

	compressed := base
	compressed.Compression = EncodingGzip
	noSigned := pipeline.PlanLane(LaneRequest{
		Plan:                compressed,
		Part:                part,
		Locality:            "internet",
		DirectObjectStore:   true,
		HTTPStreamAvailable: true,
	})
	if noSigned.Selected != LaneHTTPStream {
		t.Fatalf("compressed signed fallback lane = %+v", noSigned)
	}

	noHTTP := pipeline.PlanLane(LaneRequest{
		Plan:     base,
		Part:     part,
		Locality: "internet",
	})
	if noHTTP.Selected != LaneHTTPStream || noHTTP.Candidates[len(noHTTP.Candidates)-1].Reason == "" {
		t.Fatalf("no-http fallback lane = %+v", noHTTP)
	}
}

func acceptPipelinePart(t *testing.T, pipeline *Pipeline, ctx context.Context, transferID string, partNumber int, offset int64, payload string) transport.Envelope {
	t.Helper()
	env := transport.CreateEnvelope(EventPartAcceptRequest, map[string]any{
		"transfer_id":         transferID,
		"part_number":         partNumber,
		"offset":              offset,
		"size":                int64(len(payload)),
		"expected_raw_sha256": shaHex(payload),
		"content_type":        "text/plain",
	}, nil)
	out, err := pipeline.AcceptPartEnvelope(ctx, env, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("AcceptPartEnvelope(%d) error = %v", partNumber, err)
	}
	if out.EventType != EventPartAcceptSuccess {
		t.Fatalf("part envelope = %+v", out)
	}
	return out
}

type descriptorSource struct {
	payload string
}

func (s descriptorSource) OpenDescriptor(context.Context, PartDescriptor) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(s.payload)), nil
}

type failingDescriptorSource struct{}

func (failingDescriptorSource) OpenDescriptor(context.Context, PartDescriptor) (io.ReadCloser, error) {
	return nil, errors.New("descriptor open failed")
}

type noPresignStore struct {
	ObjectStore
}

type presignFailStore struct {
	ObjectStore
}

func (s presignFailStore) PresignPut(context.Context, string, string, time.Duration) (string, error) {
	return "", errors.New("presign failed")
}

type staticStringer string

func (s staticStringer) String() string {
	return string(s)
}

func newTestPipeline(t *testing.T, mgr *Manager) *Pipeline {
	t.Helper()
	pipeline, err := NewPipeline(PipelineOptions{
		Manager:            mgr,
		Ingress:            "runtime-transport",
		ObjectStoreBackend: "memory",
		DistributedState:   true,
		ZeroCopyAvailable:  true,
		Clock:              fixedNow,
	})
	if err != nil {
		t.Fatalf("NewPipeline() error = %v", err)
	}
	return pipeline
}

func receiptFromEnvelope(env transport.Envelope) PartReceipt {
	return PartReceipt{
		TransferID:     env.Payload["transfer_id"].(string),
		OrganizationID: env.Payload["organization_id"].(string),
		PartNumber:     env.Payload["part_number"].(int),
		RawSize:        env.Payload["raw_size"].(int64),
		RawSHA256:      env.Payload["raw_sha256"].(string),
	}
}
