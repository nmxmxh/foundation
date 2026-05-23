package bulk

import (
	"context"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	transport "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/transport"
	apperrors "github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
)

const (
	EventInitiateRequested = "bulk:transfer:initiate:v1:requested"
	EventInitiateSuccess   = "bulk:transfer:initiate:v1:success"
	EventPartAcceptRequest = "bulk:part:accept:v1:requested"
	EventPartAcceptSuccess = "bulk:part:accept:v1:success"
	EventStatusRequested   = "bulk:transfer:status:v1:requested"
	EventStatusSuccess     = "bulk:transfer:status:v1:success"
	EventCompleteRequested = "bulk:transfer:complete:v1:requested"
	EventCompleteSuccess   = "bulk:transfer:complete:v1:success"
	EventSignedPartGrant   = "bulk:part:signed_grant:v1:success"
	EventFailed            = "bulk:transfer:operation:v1:failed"
)

type Pipeline struct {
	manager            *Manager
	ingress            string
	objectStoreBackend string
	distributedState   bool
	zeroCopyAvailable  bool
	mptcpAvailable     bool
	quicAvailable      bool
	kernelPacing       bool
	attributes         map[string]string
	clock              func() time.Time
}

func NewPipeline(opts PipelineOptions) (*Pipeline, error) {
	if opts.Manager == nil {
		return nil, apperrors.New(apperrors.CodeValidation, "bulk manager is required")
	}
	if opts.Clock == nil {
		opts.Clock = func() time.Time { return time.Now().UTC() }
	}
	ingress := strings.TrimSpace(opts.Ingress)
	if ingress == "" {
		ingress = "app-owned"
	}
	backend := strings.TrimSpace(opts.ObjectStoreBackend)
	if backend == "" {
		backend = "unknown"
	}
	return &Pipeline{
		manager:            opts.Manager,
		ingress:            ingress,
		objectStoreBackend: backend,
		distributedState:   opts.DistributedState,
		zeroCopyAvailable:  opts.ZeroCopyAvailable,
		mptcpAvailable:     opts.MPTCPAvailable,
		quicAvailable:      opts.QUICAvailable,
		kernelPacing:       opts.KernelPacing,
		attributes:         cloneStringMap(opts.Attributes),
		clock:              opts.Clock,
	}, nil
}

func (p *Pipeline) HandleControl(ctx context.Context, envelope transport.Envelope) (transport.Envelope, error) {
	switch envelope.EventType {
	case EventInitiateRequested:
		req, err := decodeInitiateRequest(envelope.Payload)
		if err != nil {
			return p.failureEnvelope(envelope, "", err), err
		}
		plan, err := p.manager.Initiate(ctx, req)
		if err != nil {
			return p.failureEnvelope(envelope, req.TransferID, err), err
		}
		return p.planEnvelope(envelope, EventInitiateSuccess, plan), nil
	case EventStatusRequested:
		transferID, err := payloadString(envelope.Payload, "transfer_id", true)
		if err != nil {
			return p.failureEnvelope(envelope, "", err), err
		}
		status, err := p.manager.Status(ctx, transferID)
		if err != nil {
			return p.failureEnvelope(envelope, transferID, err), err
		}
		return p.statusEnvelope(envelope, status), nil
	case EventCompleteRequested:
		transferID, req, err := decodeCompleteRequest(envelope.Payload)
		if err != nil {
			return p.failureEnvelope(envelope, "", err), err
		}
		manifest, err := p.manager.Complete(ctx, transferID, req)
		if err != nil {
			return p.failureEnvelope(envelope, transferID, err), err
		}
		return p.manifestEnvelope(envelope, manifest), nil
	default:
		err := apperrors.New(apperrors.CodeValidation, "unsupported bulk control event")
		return p.failureEnvelope(envelope, "", err), err
	}
}

func (p *Pipeline) AcceptPartEnvelope(ctx context.Context, envelope transport.Envelope, reader io.Reader) (transport.Envelope, error) {
	if envelope.EventType != EventPartAcceptRequest {
		err := apperrors.New(apperrors.CodeValidation, "unsupported bulk part event")
		return p.failureEnvelope(envelope, "", err), err
	}
	transferID, desc, err := decodePartDescriptor(envelope.Payload)
	if err != nil {
		return p.failureEnvelope(envelope, transferID, err), err
	}
	receipt, err := p.manager.AcceptPart(ctx, transferID, desc, reader)
	if err != nil {
		return p.failureEnvelope(envelope, transferID, err), err
	}
	return p.receiptEnvelope(envelope, receipt), nil
}

func (p *Pipeline) AcceptHTTPPart(ctx context.Context, req HTTPPartRequest) (transport.Envelope, error) {
	return p.AcceptPartEnvelope(ctx, req.Envelope, req.Reader)
}

func (p *Pipeline) AcceptDescriptorPart(ctx context.Context, envelope transport.Envelope, source DescriptorSource) (transport.Envelope, error) {
	if source == nil {
		err := apperrors.New(apperrors.CodeValidation, "descriptor source is required")
		return p.failureEnvelope(envelope, "", err), err
	}
	if envelope.EventType != EventPartAcceptRequest {
		err := apperrors.New(apperrors.CodeValidation, "unsupported bulk part event")
		return p.failureEnvelope(envelope, "", err), err
	}
	transferID, desc, err := decodePartDescriptor(envelope.Payload)
	if err != nil {
		return p.failureEnvelope(envelope, transferID, err), err
	}
	reader, err := source.OpenDescriptor(ctx, desc)
	if err != nil {
		err = storePartError(err, "open descriptor part")
		return p.failureEnvelope(envelope, transferID, err), err
	}
	defer func() { _ = reader.Close() }()
	receipt, err := p.manager.AcceptPart(ctx, transferID, desc, reader)
	if err != nil {
		return p.failureEnvelope(envelope, transferID, err), err
	}
	return p.receiptEnvelope(envelope, receipt), nil
}

func (p *Pipeline) GrantSignedPart(ctx context.Context, request transport.Envelope, req SignedPartRequest) (transport.Envelope, error) {
	grant, err := p.manager.GrantSignedPart(ctx, req)
	if err != nil {
		return p.failureEnvelope(request, req.TransferID, err), err
	}
	return p.envelope(request, EventSignedPartGrant, signedPartGrantPayload(grant)), nil
}

func (p *Pipeline) AcceptSignedPart(ctx context.Context, request transport.Envelope, transferID string, desc PartDescriptor) (transport.Envelope, error) {
	receipt, err := p.manager.AcceptSignedPart(ctx, transferID, desc)
	if err != nil {
		return p.failureEnvelope(request, transferID, err), err
	}
	return p.receiptEnvelope(request, receipt), nil
}

func (p *Pipeline) Diagnostics(plan TransferPlan) LaneDiagnostics {
	risk := "none"
	if !plan.Deadline.IsZero() {
		switch remaining := plan.Deadline.Sub(p.clock()); {
		case remaining <= 0:
			risk = "expired"
		case remaining < time.Minute:
			risk = "high"
		case remaining < 5*time.Minute:
			risk = "medium"
		default:
			risk = "low"
		}
	}
	return LaneDiagnostics{
		Ingress:            p.ingress,
		ObjectStoreBackend: p.objectStoreBackend,
		ChunkSize:          plan.ChunkSize,
		Compression:        plan.Compression,
		CopyBudget:         "bounded-part-reader",
		MemoryBudget:       plan.MaxMemory,
		ResumeSupported:    true,
		DistributedState:   p.distributedState,
		ZeroCopyAvailable:  p.zeroCopyAvailable,
		MPTCPAvailable:     p.mptcpAvailable,
		QUICAvailable:      p.quicAvailable,
		KernelPacing:       p.kernelPacing,
		DeadlineRisk:       risk,
		Fallback:           "streamed-objectstore",
		Attributes:         cloneOptionalStringMap(p.attributes),
	}
}

func (p *Pipeline) planEnvelope(request transport.Envelope, eventType string, plan TransferPlan) transport.Envelope {
	payload := planPayload(plan)
	payload["lane"] = lanePayload(p.Diagnostics(plan))
	return p.envelope(request, eventType, payload)
}

func (p *Pipeline) receiptEnvelope(request transport.Envelope, receipt PartReceipt) transport.Envelope {
	return p.envelope(request, EventPartAcceptSuccess, receiptPayload(receipt))
}

func (p *Pipeline) statusEnvelope(request transport.Envelope, status TransferStatus) transport.Envelope {
	return p.envelope(request, EventStatusSuccess, statusPayload(status))
}

func (p *Pipeline) manifestEnvelope(request transport.Envelope, manifest TransferManifest) transport.Envelope {
	return p.envelope(request, EventCompleteSuccess, manifestPayload(manifest))
}

func (p *Pipeline) failureEnvelope(request transport.Envelope, transferID string, err error) transport.Envelope {
	payload := failurePayload(transferID, err)
	payload["request_event"] = request.EventType
	return p.envelope(request, EventFailed, payload)
}

func (p *Pipeline) envelope(request transport.Envelope, eventType string, payload map[string]any) transport.Envelope {
	env := transport.CreateEnvelope(eventType, payload, map[string]any{
		"bulk_pipeline": true,
	})
	if request.Metadata.CorrelationID != "" {
		env.Metadata.CorrelationID = request.Metadata.CorrelationID
	}
	if request.Metadata.RequestID != "" {
		env.Metadata.RequestID = request.Metadata.RequestID
	}
	if request.Metadata.IdempotencyKey != "" {
		env.Metadata.IdempotencyKey = request.Metadata.IdempotencyKey
	}
	if request.Metadata.SchemaVersion != "" {
		env.Metadata.SchemaVersion = request.Metadata.SchemaVersion
	}
	env.Metadata.Timestamp = p.clock()
	return env
}

func decodeInitiateRequest(payload map[string]any) (InitiateRequest, error) {
	totalSize, err := payloadInt64(payload, "total_size", true)
	if err != nil {
		return InitiateRequest{}, err
	}
	chunkSize, err := payloadInt64(payload, "chunk_size", false)
	if err != nil {
		return InitiateRequest{}, err
	}
	maxMemory, err := payloadInt64(payload, "max_memory", false)
	if err != nil {
		return InitiateRequest{}, err
	}
	maxParts, err := payloadInt(payload, "max_parts", false)
	if err != nil {
		return InitiateRequest{}, err
	}
	deadline, err := payloadTime(payload, "deadline")
	if err != nil {
		return InitiateRequest{}, err
	}
	return InitiateRequest{
		TransferID:     stringValue(payload["transfer_id"]),
		TotalSize:      totalSize,
		ChunkSize:      chunkSize,
		MaxMemory:      maxMemory,
		MaxParts:       maxParts,
		ContentType:    stringValue(payload["content_type"]),
		Compression:    stringValue(payload["compression"]),
		Deadline:       deadline,
		IdempotencyKey: stringValue(payload["idempotency_key"]),
		Attributes:     stringMapValue(payload["attributes"]),
	}, nil
}

func decodePartDescriptor(payload map[string]any) (string, PartDescriptor, error) {
	transferID, err := payloadString(payload, "transfer_id", true)
	if err != nil {
		return "", PartDescriptor{}, err
	}
	partNumber, err := payloadInt(payload, "part_number", true)
	if err != nil {
		return transferID, PartDescriptor{}, err
	}
	offset, err := payloadInt64(payload, "offset", true)
	if err != nil {
		return transferID, PartDescriptor{}, err
	}
	size, err := payloadInt64(payload, "size", true)
	if err != nil {
		return transferID, PartDescriptor{}, err
	}
	sha, err := payloadString(payload, "expected_raw_sha256", true)
	if err != nil {
		return transferID, PartDescriptor{}, err
	}
	return transferID, PartDescriptor{
		PartNumber:        partNumber,
		Offset:            offset,
		Size:              size,
		ExpectedRawSHA256: sha,
		ContentType:       stringValue(payload["content_type"]),
	}, nil
}

func decodeCompleteRequest(payload map[string]any) (string, CompleteRequest, error) {
	transferID, err := payloadString(payload, "transfer_id", true)
	if err != nil {
		return "", CompleteRequest{}, err
	}
	return transferID, CompleteRequest{
		ExpectedRootSHA256: stringValue(payload["expected_root_sha256"]),
	}, nil
}

func payloadString(payload map[string]any, key string, required bool) (string, error) {
	value := strings.TrimSpace(stringValue(payload[key]))
	if value == "" && required {
		return "", apperrors.New(apperrors.CodeValidation, key+" is required")
	}
	return value, nil
}

func payloadInt(payload map[string]any, key string, required bool) (int, error) {
	value, ok := payload[key]
	if !ok || value == nil {
		if required {
			return 0, apperrors.New(apperrors.CodeValidation, key+" is required")
		}
		return 0, nil
	}
	n, err := numberValue(value)
	if err != nil {
		return 0, apperrors.New(apperrors.CodeValidation, key+" must be numeric")
	}
	if n > maxIntValue() || n < minIntValue() {
		return 0, apperrors.New(apperrors.CodeValidation, key+" is outside integer bounds")
	}
	return int(n), nil
}

func maxIntValue() int64 {
	return int64(int(^uint(0) >> 1))
}

func minIntValue() int64 {
	return -maxIntValue() - 1
}

func payloadInt64(payload map[string]any, key string, required bool) (int64, error) {
	value, ok := payload[key]
	if !ok || value == nil {
		if required {
			return 0, apperrors.New(apperrors.CodeValidation, key+" is required")
		}
		return 0, nil
	}
	n, err := numberValue(value)
	if err != nil {
		return 0, apperrors.New(apperrors.CodeValidation, key+" must be numeric")
	}
	return n, nil
}

func numberValue(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case int32:
		return int64(typed), nil
	case float64:
		if math.Trunc(typed) != typed {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(typed), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
	default:
		return 0, fmt.Errorf("unsupported number type")
	}
}

func payloadTime(payload map[string]any, key string) (time.Time, error) {
	raw := stringValue(payload[key])
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, apperrors.New(apperrors.CodeValidation, key+" must be RFC3339")
	}
	return value, nil
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func stringMapValue(value any) map[string]string {
	switch typed := value.(type) {
	case map[string]string:
		return cloneStringMap(typed)
	case map[string]any:
		out := make(map[string]string, len(typed))
		for key, value := range typed {
			if text := stringValue(value); text != "" {
				out[key] = text
			}
		}
		return out
	default:
		return nil
	}
}
