package registry

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	eventcontract "github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"google.golang.org/protobuf/proto"
)

var allowedHTTPMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodOptions: {},
}

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

// Validate checks the declarative HTTP-to-event route shape used by routing and
// doc generation. It does not require proto request/response types because some
// routes are static or streaming, but doc-producing routes should set them.
func (r HTTPRoute) Validate() error {
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	if method == "" {
		return errors.New("http route method is required")
	}
	if _, ok := allowedHTTPMethods[method]; !ok {
		return fmt.Errorf("http route method %q is not supported", r.Method)
	}
	if strings.TrimSpace(r.Path) == "" {
		return errors.New("http route path is required")
	}
	if !strings.HasPrefix(r.Path, "/") {
		return fmt.Errorf("http route path %q must start with /", r.Path)
	}
	if strings.TrimSpace(r.EventType) == "" {
		return errors.New("http route event_type is required")
	}
	if err := eventcontract.ValidateEventType(r.EventType); err != nil {
		return err
	}
	if !strings.HasSuffix(r.EventType, ":requested") &&
		!strings.HasSuffix(r.EventType, ":success") &&
		!strings.HasSuffix(r.EventType, ":failed") {
		return fmt.Errorf("http route event_type %q must use a lifecycle terminal or requested state", r.EventType)
	}
	if r.Handler == nil && len(r.StaticPayload) == 0 {
		return errors.New("http route requires handler or static payload")
	}
	if r.SuccessStatusCode != 0 && (r.SuccessStatusCode < 200 || r.SuccessStatusCode > 299) {
		return fmt.Errorf("http route success status %d must be 2xx", r.SuccessStatusCode)
	}
	return nil
}
