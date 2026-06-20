// Package grpcx provides a gRPC connector.Driver built on server-kit's grpcsvc
// binary-frame client. It probes with the standard gRPC Health service, and
// performs request/response calls via the foundation frame dispatch.
//
// Register by importing for side effects:
//
//	import _ "github.com/nmxmxh/ovasabi_foundation/server-kit/go/connector/grpcx"
//
// then configure a transport of "grpc". Driver options (map[string]any):
//
//	"auth_token"    string         - bearer token for grpcsvc auth
//	"service"       string         - gRPC Health service name to probe ("" = server)
//	"dial_timeout"  time.Duration  - dial readiness timeout, default 5s
//	"max_message_bytes" int        - client max message size
//
// Server streaming requires a concrete service method and is not expressible
// through the generic frame client; Stream returns connector.ErrUnsupported.
package grpcx

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/connector"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
)

func init() {
	connector.Register("grpc", New)
}

// Driver is the gRPC transport.
type Driver struct {
	conn    *grpc.ClientConn
	client  *grpcsvc.Client
	health  grpc_health_v1.HealthClient
	service string
}

// New dials the target and builds a gRPC driver. It satisfies connector.Factory.
func New(endpoint string, options map[string]any) (connector.Driver, error) {
	opts := grpcsvc.ClientOptions{}
	if v, ok := options["auth_token"].(string); ok {
		opts.AuthToken = v
	}
	if v, ok := options["max_message_bytes"].(int); ok && v > 0 {
		opts.MaxMessageBytes = v
	}
	dialTimeout := 5 * time.Second
	if v, ok := options["dial_timeout"].(time.Duration); ok && v > 0 {
		dialTimeout = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	conn, err := grpcsvc.Dial(ctx, endpoint, opts)
	if err != nil {
		return nil, err
	}
	d := &Driver{
		conn:   conn,
		client: grpcsvc.NewClient(conn, opts),
		health: grpc_health_v1.NewHealthClient(conn),
	}
	if v, ok := options["service"].(string); ok {
		d.service = v
	}
	return d, nil
}

// Transport returns "grpc".
func (d *Driver) Transport() string { return "grpc" }

// Probe calls the gRPC Health service and maps its status onto the health model.
func (d *Driver) Probe(ctx context.Context) (connector.Health, error) {
	resp, err := d.health.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: d.service})
	if err != nil {
		return connector.HealthNotServing, err
	}
	switch resp.GetStatus() {
	case grpc_health_v1.HealthCheckResponse_SERVING:
		return connector.HealthServing, nil
	case grpc_health_v1.HealthCheckResponse_NOT_SERVING:
		return connector.HealthNotServing, nil
	default:
		return connector.HealthUnknown, nil
	}
}

// Capabilities reports static gRPC capabilities.
func (d *Driver) Capabilities(_ context.Context) (connector.Capabilities, error) {
	return connector.Capabilities{
		Transport: "grpc",
		Encodings: []string{"protobuf", "binary", "json"},
		Features:  []string{"health", "unary"},
		Streaming: false,
	}, nil
}

// Call dispatches a binary frame. Operation maps to the event type, Body to the
// payload, and Headers["correlation_id"] to the frame correlation id.
func (d *Driver) Call(ctx context.Context, r connector.Request) (connector.Response, error) {
	frame := grpcsvc.Frame{
		EventType:     r.Operation,
		Payload:       r.Body,
		CorrelationID: r.Headers["correlation_id"],
		SchemaVersion: r.Headers["schema_version"],
	}
	out, err := d.client.DispatchFrame(ctx, frame)
	if err != nil {
		return connector.Response{}, err
	}
	return connector.Response{
		Status:   200,
		Body:     out.Payload,
		Encoding: "binary",
		Headers: map[string]string{
			"correlation_id": out.CorrelationID,
			"schema_version": out.SchemaVersion,
		},
	}, nil
}

// Stream is unsupported through the generic frame client.
func (d *Driver) Stream(context.Context, connector.Request, string) (connector.Stream, error) {
	return nil, connector.ErrUnsupported
}

// Close closes the gRPC connection.
func (d *Driver) Close() error {
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}
