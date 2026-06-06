package contracttest

import (
	"fmt"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/worker"
)

type LifecycleObservation struct {
	Requested events.Envelope
	Terminal  events.Envelope
	Jobs      []worker.Job
}

type LifecycleOptions struct {
	RequireIdempotency bool
	RequireTenant      bool
}

func VerifyCommandLifecycle(obs LifecycleObservation, opts LifecycleOptions) error {
	requested := obs.Requested
	requested.Normalize()
	if err := requested.Validate(); err != nil {
		return fmt.Errorf("requested envelope invalid: %w", err)
	}
	if state := events.TerminalState(requested.EventType); state != "requested" {
		return fmt.Errorf("requested event state = %q, want requested", state)
	}

	requestedType, err := events.ParseEventType(requested.EventType)
	if err != nil {
		return err
	}
	correlationID := strings.TrimSpace(requested.CorrelationID)
	if correlationID == "" {
		return fmt.Errorf("requested correlation_id is required")
	}
	idempotencyKey := metadataString(requested.Metadata, "idempotency_key", "idempotencyKey")
	if opts.RequireIdempotency && idempotencyKey == "" {
		return fmt.Errorf("requested idempotency_key is required")
	}
	tenantID := metadataString(requested.Metadata, "organization_id", "organizationId")
	if opts.RequireTenant && tenantID == "" {
		return fmt.Errorf("requested organization_id is required")
	}

	if err := verifyLifecycleJobs(obs.Jobs, correlationID, idempotencyKey, tenantID, opts); err != nil {
		return err
	}
	return verifyTerminalEnvelope(obs.Terminal, requestedType, correlationID, idempotencyKey, tenantID, opts)
}

func verifyLifecycleJobs(jobs []worker.Job, correlationID, idempotencyKey, tenantID string, opts LifecycleOptions) error {
	for i := range jobs {
		job := jobs[i]
		job.Normalize()
		if err := job.Validate(); err != nil {
			return fmt.Errorf("job[%d] invalid: %w", i, err)
		}
		if strings.TrimSpace(job.CorrelationID) != correlationID {
			return fmt.Errorf("job[%d] correlation_id = %q, want %q", i, job.CorrelationID, correlationID)
		}
		if opts.RequireIdempotency && strings.TrimSpace(job.IdempotencyKey) != idempotencyKey {
			return fmt.Errorf("job[%d] idempotency_key = %q, want %q", i, job.IdempotencyKey, idempotencyKey)
		}
		if opts.RequireTenant {
			jobTenantID := metadataString(job.Metadata, "organization_id", "organizationId")
			if jobTenantID != tenantID {
				return fmt.Errorf("job[%d] organization_id = %q, want %q", i, jobTenantID, tenantID)
			}
		}
	}
	return nil
}

func verifyTerminalEnvelope(
	terminal events.Envelope,
	requestedType events.ParsedEventType,
	correlationID string,
	idempotencyKey string,
	tenantID string,
	opts LifecycleOptions,
) error {
	terminal.Normalize()
	if err := terminal.Validate(); err != nil {
		return fmt.Errorf("terminal envelope invalid: %w", err)
	}
	terminalType, err := events.ParseEventType(terminal.EventType)
	if err != nil {
		return err
	}
	if terminalType.Domain != requestedType.Domain || terminalType.Action != requestedType.Action || terminalType.Version != requestedType.Version {
		return fmt.Errorf("terminal event %q does not refine requested event %q", terminal.EventType, requestedType.Raw)
	}
	if terminalType.State != "success" && terminalType.State != "failed" {
		return fmt.Errorf("terminal event state = %q, want success or failed", terminalType.State)
	}
	if strings.TrimSpace(terminal.CorrelationID) != correlationID {
		return fmt.Errorf("terminal correlation_id = %q, want %q", terminal.CorrelationID, correlationID)
	}
	if opts.RequireIdempotency && metadataString(terminal.Metadata, "idempotency_key", "idempotencyKey") != idempotencyKey {
		return fmt.Errorf("terminal idempotency_key does not match requested idempotency_key")
	}
	if opts.RequireTenant && metadataString(terminal.Metadata, "organization_id", "organizationId") != tenantID {
		return fmt.Errorf("terminal organization_id does not match requested organization_id")
	}
	return nil
}

func metadataString(metadata extension.Object, keys ...string) string {
	for _, key := range keys {
		if value, ok := metadata.GetString(key); ok {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				return trimmed
			}
		}
	}
	for _, gcKey := range []string{"global_context", "globalContext"} {
		rawValue, ok := metadata[gcKey]
		if !ok {
			continue
		}
		raw, ok := rawValue.ObjectValue()
		if !ok {
			continue
		}
		for _, key := range keys {
			if value, ok := raw.GetString(key); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}
