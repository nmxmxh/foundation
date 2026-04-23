package registry

import (
	"net/http"

	"google.golang.org/protobuf/proto"
)

// HTTPRoute defines a REST endpoint mapped to an event_type dispatch.
type HTTPRoute struct {
	Method              string
	Path                string
	EventType           string
	Description         string
	Handler             http.HandlerFunc
	RequestSchema       string
	ResponseSchema      string
	RequiredCapability  string
	Permission          string
	RequiredQueryParams []string
	AnyOfQueryParams    [][]string
	IncludeRawBody      bool
	IncludeHeaders      []string
	StaticPayload       map[string]any
	IsStreaming         bool

	// RequestType and ResponseType enable OpenAPI schema generation from proto messages.
	// When set, docgen can introspect these types to auto-generate request/response schemas.
	RequestType  proto.Message
	ResponseType proto.Message
}
