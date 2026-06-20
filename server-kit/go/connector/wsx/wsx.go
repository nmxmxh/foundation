// Package wsx provides a WebSocket connector.Driver built on
// golang.org/x/net/websocket. It probes by establishing (and closing) a
// connection, performs request/response calls over a short-lived socket, and
// streams messages over a persistent socket with watermark-based resume.
//
// Register by importing for side effects:
//
//	import _ "github.com/nmxmxh/ovasabi_foundation/server-kit/go/connector/wsx"
//
// then configure a transport of "websocket". Driver options (map[string]any):
//
//	"origin"  string            - Origin header, default "http://localhost"
//	"headers" map[string]string - headers sent on the upgrade request
//	"timeout" time.Duration     - dial/call timeout, default 30s
//
// Because raw WebSocket frames carry no transport-level cursor, a watermark is
// only produced when a message is a JSON object containing an "id" or
// "watermark" field; on resume that token is sent as the first frame so the
// remote can replay from it.
package wsx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/websocket"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/connector"
)

func init() {
	connector.Register("websocket", New)
}

// Driver is the WebSocket transport.
type Driver struct {
	endpoint string
	origin   string
	headers  http.Header
	timeout  time.Duration
}

// New builds a WebSocket driver. It satisfies connector.Factory.
func New(endpoint string, options map[string]any) (connector.Driver, error) {
	d := &Driver{
		endpoint: endpoint,
		origin:   "http://localhost",
		headers:  http.Header{},
		timeout:  30 * time.Second,
	}
	if v, ok := options["origin"].(string); ok && v != "" {
		d.origin = v
	}
	if v, ok := options["timeout"].(time.Duration); ok && v > 0 {
		d.timeout = v
	}
	if v, ok := options["headers"].(map[string]string); ok {
		for k, val := range v {
			d.headers.Set(k, val)
		}
	}
	return d, nil
}

// Transport returns "websocket".
func (d *Driver) Transport() string { return "websocket" }

func (d *Driver) dial(ctx context.Context, op string) (*websocket.Conn, error) {
	target := d.endpoint
	if op != "" {
		u, err := url.Parse(d.endpoint)
		if err != nil {
			return nil, err
		}
		u = u.JoinPath(op)
		target = u.String()
	}
	cfg, err := websocket.NewConfig(target, d.origin)
	if err != nil {
		return nil, err
	}
	if len(d.headers) > 0 {
		cfg.Header = d.headers.Clone()
	}
	type result struct {
		conn *websocket.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, derr := cfg.DialContext(ctx)
		ch <- result{conn, derr}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.conn, r.err
	}
}

// Probe dials the endpoint and immediately closes it. A successful upgrade means
// serving; a failure means not serving.
func (d *Driver) Probe(ctx context.Context) (connector.Health, error) {
	pctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	conn, err := d.dial(pctx, "")
	if err != nil {
		return connector.HealthNotServing, err
	}
	_ = conn.Close()
	return connector.HealthServing, nil
}

// Capabilities reports static WebSocket capabilities.
func (d *Driver) Capabilities(_ context.Context) (connector.Capabilities, error) {
	return connector.Capabilities{
		Transport: "websocket",
		Encodings: []string{"json", "binary"},
		Features:  []string{"duplex", "stream"},
		Streaming: true,
	}, nil
}

// Call opens a short-lived socket, sends the request body, and reads one reply.
func (d *Driver) Call(ctx context.Context, r connector.Request) (connector.Response, error) {
	cctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	conn, err := d.dial(cctx, r.Operation)
	if err != nil {
		return connector.Response{}, err
	}
	defer conn.Close()
	if dl, ok := cctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if len(r.Body) > 0 {
		if err := websocket.Message.Send(conn, r.Body); err != nil {
			return connector.Response{}, err
		}
	}
	var data []byte
	if err := websocket.Message.Receive(conn, &data); err != nil {
		return connector.Response{}, err
	}
	return connector.Response{Status: 200, Body: data, Encoding: "json"}, nil
}

// Stream opens a persistent socket. When resume is set it is sent as the first
// frame so the remote can replay from the watermark.
func (d *Driver) Stream(ctx context.Context, r connector.Request, resume string) (connector.Stream, error) {
	conn, err := d.dial(ctx, r.Operation)
	if err != nil {
		return nil, err
	}
	if resume != "" {
		_ = websocket.Message.Send(conn, []byte(resume))
	} else if len(r.Body) > 0 {
		_ = websocket.Message.Send(conn, r.Body)
	}
	return &wsStream{conn: conn}, nil
}

// Close is a no-op; sockets are owned per-call/per-stream.
func (d *Driver) Close() error { return nil }

type wsStream struct {
	conn   *websocket.Conn
	lastWM string
}

func (s *wsStream) Recv() (connector.StreamMessage, error) {
	var data []byte
	if err := websocket.Message.Receive(s.conn, &data); err != nil {
		return connector.StreamMessage{}, err
	}
	wm := extractWatermark(data)
	if wm != "" {
		s.lastWM = wm
	}
	return connector.StreamMessage{Data: data, Encoding: "json", Watermark: wm}, nil
}

func (s *wsStream) Watermark() string { return s.lastWM }

func (s *wsStream) Close() error { return s.conn.Close() }

// extractWatermark pulls a resume token from a JSON-object frame, if present.
func extractWatermark(data []byte) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return ""
	}
	for _, key := range []string{"watermark", "id"} {
		if raw, ok := obj[key]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil && s != "" {
				return s
			}
		}
	}
	return ""
}
