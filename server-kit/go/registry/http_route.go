package registry

import (
	"net/http"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"google.golang.org/protobuf/proto"
)

// HTTPSecurityRequirement describes one OpenAPI security requirement entry.
type HTTPSecurityRequirement struct {
	Scheme string
	Scopes []string
}

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
	StaticPayload       extension.Object
	Metadata            extension.Object
	Tags                []string
	IsStreaming         bool
	IsPublic            bool
	AuthRequirements    []HTTPSecurityRequirement
	SuccessStatusCode   int
	SuccessDescription  string
	NoContentResponse   bool

	// RequestType and ResponseType enable OpenAPI schema generation from proto messages.
	// When set, docgen can introspect these types to auto-generate request/response schemas.
	RequestType  proto.Message
	ResponseType proto.Message
}
