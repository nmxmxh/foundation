package connector

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
)

// Errors a Driver (or the core) may return. Drivers should wrap transport
// errors so callers can classify them without importing protocol packages.
var (
	// ErrUnsupported is returned by optional Driver methods a transport cannot
	// provide (e.g. Stream on a request/response-only transport).
	ErrUnsupported = errors.New("connector: operation unsupported by transport")
	// ErrNotRegistered is returned when no factory is registered for a transport.
	ErrNotRegistered = errors.New("connector: transport not registered")
	// ErrNoTransport is returned when a connector has no usable transport left.
	ErrNoTransport = errors.New("connector: no usable transport")
	// ErrClosed is returned once a connector or stream has been closed.
	ErrClosed = errors.New("connector: closed")
)

// Request is a transport-agnostic outbound request. Drivers interpret the
// fields they understand; unknown fields are ignored. Operation is the logical
// remote operation (an HTTP path, a gRPC full-method, a GraphQL operation name)
// and is what metrics, chaos targeting, and capability lookups key on.
type Request struct {
	Operation string            // logical operation id (path / method / op name)
	Method    string            // verb where meaningful (HTTP method, etc.)
	Headers   map[string]string // transport headers / metadata
	Query     map[string]string // query params / structured args
	Body      []byte            // encoded payload
	Encoding  string            // payload encoding hint (json, protobuf, ...)
}

// Response is a transport-agnostic outbound response.
type Response struct {
	Status   int               // normalized status (HTTP-style; 0 if N/A)
	Headers  map[string]string // response headers / trailers
	Body     []byte            // encoded payload
	Encoding string            // payload encoding of Body
}

// StreamMessage is one frame of a streaming response. Watermark, when non-empty,
// is an opaque resume token (cursor / Last-Event-ID / offset) the connector
// stores so it can resume the stream after a disconnect.
type StreamMessage struct {
	Data      []byte
	Encoding  string
	Watermark string
	EventType string
}

// Stream is a server-streaming response handle. Recv blocks for the next
// message and returns io.EOF when the stream completes normally.
type Stream interface {
	Recv() (StreamMessage, error)
	// Watermark returns the resume token of the most recently received message.
	Watermark() string
	Close() error
}

// Capabilities is what a connector knows about a remote: the protocol it speaks,
// the encodings and features it advertises, and its schema/API version. It is
// negotiated once (when cheap) and cached, refreshed by the supervisor.
type Capabilities struct {
	Transport string   `json:"transport"`
	Version   string   `json:"version,omitempty"`
	Encodings []string `json:"encodings,omitempty"`
	Features  []string `json:"features,omitempty"`
	Streaming bool     `json:"streaming"`
}

// Supports reports whether the named feature is advertised.
func (c Capabilities) Supports(feature string) bool {
	return slices.Contains(c.Features, feature)
}

// Driver is the transport plug. Each protocol (REST, gRPC, WebSocket, GraphQL,
// ...) provides one. The core package depends only on this interface, never on
// any concrete driver, so transports compose without modifying core.
type Driver interface {
	// Transport returns the stable transport name (e.g. "http", "grpc").
	Transport() string
	// Probe performs an active health check (HTTP /healthz, gRPC Health/Check,
	// WS ping/pong, GraphQL introspection ping). It must be cheap and bounded
	// by ctx; it must not mutate remote state.
	Probe(ctx context.Context) (Health, error)
	// Capabilities negotiates/returns what the remote advertises. It may be
	// derived from a probe; implementations should make it cheap to call.
	Capabilities(ctx context.Context) (Capabilities, error)
	// Call performs a single request/response exchange.
	Call(ctx context.Context, req Request) (Response, error)
	// Stream opens a server stream. Implementations that cannot stream return
	// ErrUnsupported. resume, when non-empty, is a watermark to resume from.
	Stream(ctx context.Context, req Request, resume string) (Stream, error)
	// Close releases any persistent connections/clients held by the driver.
	Close() error
}

// Factory builds a Driver for a target endpoint with driver-specific options.
// Options is an opaque bag interpreted by each factory.
type Factory func(endpoint string, options map[string]any) (Driver, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register makes a transport available by name. Driver subpackages call this
// from init(). Re-registering a name overwrites the previous factory.
func Register(transport string, factory Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[transport] = factory
}

// NewDriver constructs a registered driver, or returns ErrNotRegistered.
func NewDriver(transport, endpoint string, options map[string]any) (Driver, error) {
	registryMu.RLock()
	factory, ok := registry[transport]
	registryMu.RUnlock()
	if !ok {
		return nil, ErrNotRegistered
	}
	return factory(endpoint, options)
}

// Transports lists the registered transport names in sorted order.
func Transports() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
