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
//
// A route is answerable in exactly three ways, and the check is built around
// that rather than around any one of them:
//
//   - registry dispatch — no Handler; the server synthesizes one and looks the
//     work up by EventType, so the event type is what makes the route answerable
//     and is therefore required (this is what MakeEventRoute produces, and it is
//     the common case)
//   - its own http.HandlerFunc — answered directly, never consulting the
//     registry, so EventType is optional and purely descriptive
//   - a StaticPayload — answered from the declaration itself
//
// Requiring both an EventType and a Handler/StaticPayload on every route, as an
// earlier version did, described none of these three. It rejected the canonical
// dispatch route — the shape of nearly every route a project declares — while
// also rejecting the directly served, event-less route that the server
// (registerDomainRoutes) and the registry (VerifyRoutes) both explicitly
// support. Nothing called Validate, so the contradiction stayed invisible.
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

	eventType := strings.TrimSpace(r.EventType)
	servesItself := r.Handler != nil || len(r.StaticPayload) > 0
	if eventType == "" && !servesItself {
		return errors.New("http route requires an event_type, a handler, or a static payload")
	}
	// Grammar is checked whenever an event type is present, including on
	// directly served routes: it is still published in the lifecycle catalogue
	// and the OpenAPI document, so a malformed one is a docs defect even when it
	// is never dispatched.
	if eventType != "" {
		if err := eventcontract.ValidateEventType(eventType); err != nil {
			return err
		}
		if !strings.HasSuffix(eventType, ":requested") &&
			!strings.HasSuffix(eventType, ":success") &&
			!strings.HasSuffix(eventType, ":failed") {
			return fmt.Errorf("http route event_type %q must use a lifecycle terminal or requested state", r.EventType)
		}
		// Only a dispatched route is looked up by event type, and the registry
		// accepts nothing but :requested. A terminal state here would name a
		// registration that can never exist.
		if !servesItself && !strings.HasSuffix(eventType, ":requested") {
			return fmt.Errorf("http route event_type %q is dispatched and must end with :requested", r.EventType)
		}
	}

	if r.SuccessStatusCode != 0 && (r.SuccessStatusCode < 200 || r.SuccessStatusCode > 299) {
		return fmt.Errorf("http route success status %d must be 2xx", r.SuccessStatusCode)
	}
	return nil
}
