// Package httpx provides a REST/HTTP connector.Driver. It probes a health path,
// performs request/response calls, and streams via Server-Sent Events (SSE) with
// Last-Event-ID based resumption.
//
// Register it by importing for side effects:
//
//	import _ "github.com/nmxmxh/ovasabi_foundation/server-kit/go/connector/httpx"
//
// then configure a transport of "http". Driver options (map[string]any):
//
//	"health_path" string        - probe path, default "/healthz"
//	"timeout"     time.Duration  - per-call timeout, default 30s
//	"headers"     map[string]string - headers applied to every request
package httpx

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/connector"
)

func init() {
	connector.Register("http", New)
}

// Driver is the REST/HTTP transport.
type Driver struct {
	base       string
	healthPath string
	headers    map[string]string
	client     *http.Client
}

// New builds an HTTP driver. It satisfies connector.Factory.
func New(endpoint string, options map[string]any) (connector.Driver, error) {
	d := &Driver{
		base:       strings.TrimRight(endpoint, "/"),
		healthPath: "/healthz",
		headers:    map[string]string{},
		client:     &http.Client{Timeout: 30 * time.Second},
	}
	if v, ok := options["health_path"].(string); ok && v != "" {
		d.healthPath = v
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

// Transport returns "http".
func (d *Driver) Transport() string { return "http" }

// Probe issues GET healthPath. 2xx/3xx/4xx<500 => serving (reachable), 5xx =>
// not serving, transport error => not serving.
func (d *Driver) Probe(ctx context.Context) (connector.Health, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+d.healthPath, nil)
	if err != nil {
		return connector.HealthUnknown, err
	}
	d.applyHeaders(req)
	resp, err := d.client.Do(req)
	if err != nil {
		return connector.HealthNotServing, err
	}
	defer drain(resp.Body)
	switch {
	case resp.StatusCode >= 500:
		return connector.HealthNotServing, fmt.Errorf("httpx: probe status %d", resp.StatusCode)
	case resp.StatusCode == 429 || resp.StatusCode == 503:
		return connector.HealthDegraded, nil
	default:
		return connector.HealthServing, nil
	}
}

// Capabilities reports the static HTTP capabilities.
func (d *Driver) Capabilities(_ context.Context) (connector.Capabilities, error) {
	return connector.Capabilities{
		Transport: "http",
		Encodings: []string{"json", "binary"},
		Features:  []string{"sse"},
		Streaming: true,
	}, nil
}

// Call performs a single HTTP exchange. The request Method defaults to GET when
// Body is empty, POST otherwise. Operation is treated as a path appended to the
// base endpoint.
func (d *Driver) Call(ctx context.Context, r connector.Request) (connector.Response, error) {
	method := r.Method
	if method == "" {
		if len(r.Body) > 0 {
			method = http.MethodPost
		} else {
			method = http.MethodGet
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, d.url(r), bodyReader(r.Body))
	if err != nil {
		return connector.Response{}, err
	}
	d.applyHeaders(req)
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	if r.Encoding == "json" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return connector.Response{}, err
	}
	defer drain(resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return connector.Response{}, err
	}
	out := connector.Response{
		Status:   resp.StatusCode,
		Headers:  flatten(resp.Header),
		Body:     body,
		Encoding: encodingOf(resp.Header.Get("Content-Type")),
	}
	if resp.StatusCode >= 500 {
		return out, fmt.Errorf("httpx: status %d", resp.StatusCode)
	}
	return out, nil
}

// Stream opens an SSE stream. resume, when set, is sent as Last-Event-ID so the
// remote can replay from the watermark.
func (d *Driver) Stream(ctx context.Context, r connector.Request, resume string) (connector.Stream, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url(r), nil)
	if err != nil {
		return nil, err
	}
	d.applyHeaders(req)
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	if resume != "" {
		req.Header.Set("Last-Event-ID", resume)
	}
	// Use a client without the per-call timeout so the stream can stay open.
	client := &http.Client{Transport: d.client.Transport}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		drain(resp.Body)
		return nil, fmt.Errorf("httpx: stream status %d", resp.StatusCode)
	}
	return &sseStream{resp: resp, r: bufio.NewReader(resp.Body), lastID: resume}, nil
}

// Close releases idle connections.
func (d *Driver) Close() error {
	d.client.CloseIdleConnections()
	return nil
}

func (d *Driver) url(r connector.Request) string {
	u := d.base
	if r.Operation != "" {
		if !strings.HasPrefix(r.Operation, "/") {
			u += "/"
		}
		u += r.Operation
	}
	if len(r.Query) > 0 {
		parts := make([]string, 0, len(r.Query))
		for k, v := range r.Query {
			parts = append(parts, k+"="+v)
		}
		u += "?" + strings.Join(parts, "&")
	}
	return u
}

func (d *Driver) applyHeaders(req *http.Request) {
	for k, v := range d.headers {
		req.Header.Set(k, v)
	}
}

// sseStream parses a text/event-stream into connector.StreamMessages. The "id"
// field becomes the watermark; "event" becomes EventType; "data" lines join.
type sseStream struct {
	resp   *http.Response
	r      *bufio.Reader
	lastID string
}

func (s *sseStream) Recv() (connector.StreamMessage, error) {
	var (
		data  bytes.Buffer
		event string
		id    = s.lastID
		got   bool
	)
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			if got && len(data.Bytes()) > 0 {
				s.lastID = id
				return connector.StreamMessage{Data: trimTrailingNL(data.Bytes()), Encoding: "json", Watermark: id, EventType: event}, nil
			}
			return connector.StreamMessage{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" { // dispatch on blank line
			if !got {
				continue
			}
			s.lastID = id
			return connector.StreamMessage{Data: trimTrailingNL(data.Bytes()), Encoding: "json", Watermark: id, EventType: event}, nil
		}
		if strings.HasPrefix(line, ":") { // comment / heartbeat
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			id = value
			got = true
		case "event":
			event = value
			got = true
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
			got = true
		}
	}
}

func (s *sseStream) Watermark() string { return s.lastID }

func (s *sseStream) Close() error {
	if s.resp != nil {
		return s.resp.Body.Close()
	}
	return nil
}

func bodyReader(b []byte) io.Reader {
	if len(b) == 0 {
		return nil
	}
	return bytes.NewReader(b)
}

func drain(rc io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(rc, 1<<16))
	_ = rc.Close()
}

func flatten(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k := range h {
		out[k] = h.Get(k)
	}
	return out
}

func encodingOf(contentType string) string {
	if strings.Contains(contentType, "json") {
		return "json"
	}
	if contentType == "" {
		return ""
	}
	return "binary"
}

func trimTrailingNL(b []byte) []byte {
	return bytes.TrimRight(b, "\n")
}
