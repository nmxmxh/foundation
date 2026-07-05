package graceful

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/logger"
)

// stubTx satisfies pgx.Tx through interface embedding; the emitter only
// checks for a non-nil transaction, so no method is ever invoked.
type stubTx struct{ pgx.Tx }

type failingEmitter struct{}

func (failingEmitter) EmitEvent(context.Context, string, extension.Object, extension.Object) error {
	return errors.New("emitter down")
}

func (failingEmitter) EmitEventTx(context.Context, pgx.Tx, string, extension.Object, extension.Object) error {
	return errors.New("emitter down")
}

func TestToObjectNilReceiversAndPickIdempotency(t *testing.T) {
	if got := (*SuccessContext)(nil).ToObject(); len(got) != 0 {
		t.Fatalf("nil SuccessContext.ToObject() = %+v", got)
	}
	if got := (*ErrorContext)(nil).ToObject(); len(got) != 0 {
		t.Fatalf("nil ErrorContext.ToObject() = %+v", got)
	}
	if got := pickIdempotency(nil); got != "" {
		t.Fatalf("pickIdempotency(nil) = %q", got)
	}
}

func TestErrorContextToObjectFields(t *testing.T) {
	ec := &ErrorContext{Code: "error", Message: "failed", Cause: "boom", Service: "svc"}
	obj := ec.ToObject()
	if code, _ := obj.GetString("code"); code != "error" {
		t.Fatalf("code = %q", code)
	}
	if service, _ := obj.GetString("service"); service != "svc" {
		t.Fatalf("service = %q", service)
	}
}

func TestRedisEmitterRejectsTransactionalEmit(t *testing.T) {
	emitter := NewRedisEventEmitter(eventcontract.NewInMemoryBus(4))
	err := emitter.EmitEventTx(context.Background(), stubTx{}, "orders:create:v1:success", nil, nil)
	if !errors.Is(err, ErrEmitTxUnsupported) {
		t.Fatalf("EmitEventTx(tx) error = %v, want ErrEmitTxUnsupported", err)
	}
}

func TestRiverEmitterRespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	emitter := NewRiverEventEmitter(nil)
	if err := emitter.EmitEvent(ctx, "orders:create:v1:success", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("EmitEvent error = %v, want context.Canceled", err)
	}
	if err := emitter.EmitEventTx(ctx, nil, "orders:create:v1:success", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("EmitEventTx error = %v, want context.Canceled", err)
	}
}

func TestHandlerWarnsWhenEmitFails(t *testing.T) {
	var out bytes.Buffer
	log, err := logger.New(logger.Config{
		Environment: "development",
		LogLevel:    "debug",
		Format:      logger.FormatJSON,
		ServiceName: "svc",
		Component:   "graceful",
		Output:      &out,
	})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	handler := NewHandler(WithLogger(log), WithEventEmitter(failingEmitter{}), WithEventEnabled(true))
	handler.Success(context.Background(), "orders:create:v1:requested", "ok", nil, nil, "o1", nil)
	handler.Error(context.Background(), "orders:create:v1:requested", "bad", errors.New("boom"), nil, "o1")
	got := out.String()
	if !strings.Contains(got, "success event emit failed") {
		t.Fatalf("missing success emit warning in %q", got)
	}
	if !strings.Contains(got, "failure event emit failed") {
		t.Fatalf("missing failure emit warning in %q", got)
	}
}
