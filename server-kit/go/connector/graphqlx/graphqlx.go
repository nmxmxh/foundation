// Package graphqlx provides a GraphQL-over-HTTP connector.Driver. It probes with
// a trivial query, performs operations via POST, and derives capability
// awareness from a bounded introspection query. Subscriptions (which require a
// streaming transport) are not handled here; pair the connector with a wsx lane
// for those.
//
// Register by importing for side effects:
//
//	import _ "github.com/nmxmxh/ovasabi_foundation/server-kit/go/connector/graphqlx"
//
// then configure a transport of "graphql". Driver options (map[string]any):
//
//	"timeout" time.Duration         - per-call timeout, default 30s
//	"headers" map[string]string     - headers applied to every request
package graphqlx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/connector"
)

func init() {
	connector.Register("graphql", New)
}

// Driver is the GraphQL-over-HTTP transport.
type Driver struct {
	endpoint string
	headers  map[string]string
	client   *http.Client
}

// New builds a GraphQL driver. It satisfies connector.Factory.
func New(endpoint string, options map[string]any) (connector.Driver, error) {
	d := &Driver{
		endpoint: endpoint,
		headers:  map[string]string{},
		client:   &http.Client{Timeout: 30 * time.Second},
	}
	if v, ok := options["timeout"].(time.Duration); ok && v > 0 {
		d.client.Timeout = v
	}
	if v, ok := options["headers"].(map[string]string); ok {
		maps.Copy(d.headers, v)
	}
	if v, ok := options["client"].(*http.Client); ok && v != nil {
		d.client = v
	}
	return d, nil
}

// Transport returns "graphql".
func (d *Driver) Transport() string { return "graphql" }

type gqlRequest struct {
	Query         string          `json:"query"`
	OperationName string          `json:"operationName,omitempty"`
	Variables     json.RawMessage `json:"variables,omitempty"`
}

type gqlError struct {
	Message string `json:"message"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors,omitempty"`
}

// Probe runs `{__typename}`. A 200 with no GraphQL errors means serving.
func (d *Driver) Probe(ctx context.Context) (connector.Health, error) {
	_, gqlErrs, status, err := d.exec(ctx, gqlRequest{Query: "{__typename}"})
	if err != nil {
		return connector.HealthNotServing, err
	}
	if status >= 500 {
		return connector.HealthNotServing, fmt.Errorf("graphqlx: probe status %d", status)
	}
	if len(gqlErrs) > 0 {
		return connector.HealthDegraded, nil
	}
	return connector.HealthServing, nil
}

// Capabilities runs a bounded introspection query to learn the schema's query
// type name and whether subscriptions exist.
func (d *Driver) Capabilities(ctx context.Context) (connector.Capabilities, error) {
	caps := connector.Capabilities{Transport: "graphql", Encodings: []string{"json"}}
	const q = `{__schema{queryType{name} mutationType{name} subscriptionType{name}}}`
	data, gqlErrs, _, err := d.exec(ctx, gqlRequest{Query: q})
	if err != nil || len(gqlErrs) > 0 {
		return caps, nil // introspection may be disabled; not fatal
	}
	var parsed struct {
		Schema struct {
			QueryType        *struct{ Name string } `json:"queryType"`
			MutationType     *struct{ Name string } `json:"mutationType"`
			SubscriptionType *struct{ Name string } `json:"subscriptionType"`
		} `json:"__schema"`
	}
	if json.Unmarshal(data, &parsed) == nil {
		if parsed.Schema.QueryType != nil {
			caps.Features = append(caps.Features, "query")
		}
		if parsed.Schema.MutationType != nil {
			caps.Features = append(caps.Features, "mutation")
		}
		if parsed.Schema.SubscriptionType != nil {
			caps.Features = append(caps.Features, "subscription")
		}
	}
	return caps, nil
}

// Call executes a GraphQL operation. The request Body, when present, is sent as
// the raw GraphQL JSON ({query, variables, operationName}); otherwise a request
// is built from Query["query"], Query["variables"], and Operation.
func (d *Driver) Call(ctx context.Context, r connector.Request) (connector.Response, error) {
	var payload []byte
	if len(r.Body) > 0 {
		payload = r.Body
	} else {
		req := gqlRequest{Query: r.Query["query"], OperationName: r.Operation}
		if v := r.Query["variables"]; v != "" {
			req.Variables = json.RawMessage(v)
		}
		b, err := json.Marshal(req)
		if err != nil {
			return connector.Response{}, err
		}
		payload = b
	}
	body, status, err := d.post(ctx, payload, r.Headers)
	if err != nil {
		return connector.Response{}, err
	}
	out := connector.Response{Status: status, Body: body, Encoding: "json"}
	if status >= 500 {
		return out, fmt.Errorf("graphqlx: status %d", status)
	}
	// GraphQL surfaces operation errors in a 200 body; reflect that as an error
	// so the connector's health model and retries see it.
	var resp gqlResponse
	if json.Unmarshal(body, &resp) == nil && len(resp.Errors) > 0 {
		return out, fmt.Errorf("graphqlx: %s", resp.Errors[0].Message)
	}
	return out, nil
}

// Stream is unsupported over plain HTTP GraphQL; use a wsx lane for subscriptions.
func (d *Driver) Stream(context.Context, connector.Request, string) (connector.Stream, error) {
	return nil, connector.ErrUnsupported
}

// Close releases idle connections.
func (d *Driver) Close() error {
	d.client.CloseIdleConnections()
	return nil
}

func (d *Driver) exec(ctx context.Context, req gqlRequest) (json.RawMessage, []gqlError, int, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, nil, 0, err
	}
	body, status, err := d.post(ctx, payload, nil)
	if err != nil {
		return nil, nil, status, err
	}
	var resp gqlResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, status, err
	}
	return resp.Data, resp.Errors, status, nil
}

func (d *Driver) post(ctx context.Context, payload []byte, extra map[string]string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range d.headers {
		req.Header.Set(k, v)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}
