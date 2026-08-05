package appbench

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/grpcsvc"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/httpapi"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/registry"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// The null lane measures what Foundation costs when the domain does nothing.
//
// Every benchmark in this file runs the same empty operation — a handler that
// returns immediately — at an ascending rung of the transport ladder described
// in docs/performance_practices.md ("Network and transport performance").
// Rung 00 is a bare Go call, the floor no framework can beat. Each rung above
// it adds exactly one layer of Foundation. The distance from rung 00 to the top
// rung is the framework tax: the latency and allocation budget consumed before
// a single line of product code runs.
//
// This is the "empty program that only forwards packets" measurement. It
// answers a question no per-component benchmark can: if the floor already eats
// the latency budget, no amount of domain optimization can recover it, and the
// correct response is to remove a layer rather than tune one.
//
// Measurement hygiene: requests, response writers, and bodies are constructed
// once and reset per iteration, so the numbers attribute cost to Foundation
// rather than to net/http and httptest scaffolding. That makes these benchmarks
// a floor, not a model of production traffic — a real request additionally pays
// connection, TLS, parsing, and syscall costs the null lane deliberately
// excludes. Compare rungs against each other, never against service-backed
// latency.
//
// Results are recorded in docs/foundation_benchmarks.md. Allocation and byte
// budgets are gated by tooling/scripts/benchmark_ratchet_check.sh.

const nullLaneEventType = "appbench.null.echo"

// nullBody is a reusable io.ReadCloser over a fixed payload. Resetting it
// between iterations keeps request-body plumbing out of the allocation profile.
type nullBody struct {
	reader  *bytes.Reader
	payload []byte
}

func newNullBody(payload []byte) *nullBody {
	return &nullBody{reader: bytes.NewReader(payload), payload: payload}
}

func (b *nullBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (b *nullBody) Close() error               { return nil }
func (b *nullBody) reset()                     { b.reader.Reset(b.payload) }

// nullResponseWriter discards everything. It reuses one header map so that
// middleware setting response headers does not reallocate every iteration; the
// lane measures Foundation's steady-state cost, not map growth.
type nullResponseWriter struct {
	header http.Header
	status int
}

func newNullResponseWriter() *nullResponseWriter {
	return &nullResponseWriter{header: make(http.Header, 8)}
}

func (w *nullResponseWriter) Header() http.Header         { return w.header }
func (w *nullResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *nullResponseWriter) WriteHeader(status int)      { w.status = status }

// nullRouter registers a frame handler that returns its input untouched.
func nullRouter(b *testing.B) *grpcsvc.Router {
	b.Helper()
	router := grpcsvc.NewRouter()
	err := router.RegisterFrame(nullLaneEventType, func(_ context.Context, frame grpcsvc.Frame) (grpcsvc.Frame, error) {
		return frame, nil
	})
	if err != nil {
		b.Fatalf("register null frame handler: %v", err)
	}
	return router
}

func nullRoute() registry.HTTPRoute {
	return registry.HTTPRoute{
		Method:    http.MethodPost,
		Path:      "/v1/null",
		EventType: nullLaneEventType,
	}
}

// nullExecutor is a DispatchExecutor that does nothing but acknowledge.
func nullExecutor(w http.ResponseWriter, _ *http.Request, _ httpapi.DispatchRequest) {
	w.WriteHeader(http.StatusNoContent)
}

func nullRequest(b *testing.B, body *nullBody) *http.Request {
	b.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/null", body)
	req.Header.Set("Content-Type", "application/json")
	// A real server sets ContentLength from the request header. httptest only
	// infers it for the reader types it recognizes, and nullBody is not one of
	// them, so without this the lane measures the chunked-transfer path that
	// almost no client uses — and hides whether ingress sizes its read buffer.
	req.ContentLength = int64(len(body.payload))
	return req
}

// -- Rung 00: the floor -------------------------------------------------------

// BenchmarkNullLane_00_GoCall is the theoretical minimum: an indirect call
// through a function value, which is what every rung above ultimately performs.
// Read every other number in this file as a multiple of this one.
func BenchmarkNullLane_00_GoCall(b *testing.B) {
	handler := func(frame grpcsvc.Frame) (grpcsvc.Frame, error) { return frame, nil }
	frame := grpcsvc.Frame{EventType: nullLaneEventType, Payload: []byte(`{}`)}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := handler(frame); err != nil {
			b.Fatal(err)
		}
	}
}

// -- Rungs 01-02: same-process frame dispatch --------------------------------

// BenchmarkNullLane_01_RouterFrameDispatch measures router lookup and dispatch
// with no client-side validation: the cost of routing an event type to its
// owner, and the cheapest Foundation-mediated boundary there is.
func BenchmarkNullLane_01_RouterFrameDispatch(b *testing.B) {
	router := nullRouter(b)
	frame := grpcsvc.Frame{EventType: nullLaneEventType, Payload: []byte(`{}`)}
	ctx := context.Background()

	b.ReportAllocs()

	for b.Loop() {
		if _, err := router.DispatchFrame(ctx, frame); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNullLane_02_DirectFrameClient adds the frame validation the direct
// client performs before dispatch. The delta against rung 01 is the price of
// the safety check on the same-process hot path.
func BenchmarkNullLane_02_DirectFrameClient(b *testing.B) {
	client := grpcsvc.NewDirectFrameClient(nullRouter(b), grpcsvc.ServerOptions{})
	frame := grpcsvc.Frame{EventType: nullLaneEventType, Payload: []byte(`{}`)}
	ctx := context.Background()

	b.ReportAllocs()

	for b.Loop() {
		if _, err := client.DispatchFrame(ctx, frame); err != nil {
			b.Fatal(err)
		}
	}
}

// -- Rungs 03-05: HTTP ingress ------------------------------------------------

// BenchmarkNullLane_03_HTTPDispatchBuild measures only the ingress translation:
// HTTP request in, canonical DispatchRequest out. No handler, no middleware.
func BenchmarkNullLane_03_HTTPDispatchBuild(b *testing.B) {
	plan := httpapi.CompileDispatchRoute(nullRoute())
	body := newNullBody([]byte(`{}`))
	req := nullRequest(b, body)

	b.ReportAllocs()

	for b.Loop() {
		body.reset()
		if _, err := plan.Build(req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNullLane_04_HTTPRouteHandler is the full HTTP ingress path with an
// empty executor: method check, dispatch build, executor invocation. This is
// the closest analogue to "an empty program that only forwards packets" — what
// a Foundation HTTP endpoint pays before doing any work at all.
func BenchmarkNullLane_04_HTTPRouteHandler(b *testing.B) {
	handler := httpapi.NewEventRouteHandler(nullRoute(), nullExecutor)
	body := newNullBody([]byte(`{}`))
	req := nullRequest(b, body)
	w := newNullResponseWriter()

	b.ReportAllocs()

	for b.Loop() {
		body.reset()
		handler.ServeHTTP(w, req)
	}
}

// BenchmarkNullLane_05_HTTPRouteHandlerCorrelated adds correlation-ID
// propagation, which every observable Foundation route carries.
func BenchmarkNullLane_05_HTTPRouteHandlerCorrelated(b *testing.B) {
	handler := httpapi.CorrelationMiddleware(
		httpapi.NewEventRouteHandler(nullRoute(), nullExecutor),
	)
	body := newNullBody([]byte(`{}`))
	req := nullRequest(b, body)
	req.Header.Set("X-Correlation-ID", "corr-null")
	w := newNullResponseWriter()

	b.ReportAllocs()

	for b.Loop() {
		body.reset()
		handler.ServeHTTP(w, req)
	}
}

// -- Rung 06: the real floor for an authenticated endpoint --------------------

// BenchmarkNullLane_06_HTTPSecuredChain is the honest floor for a production
// route: security headers, input validation, JWT authentication, capability
// authorization, correlation, ingress translation, and an empty executor.
//
// The distance between this and rung 00 is the Foundation tax. Any change that
// widens it spends the product's latency budget on framework overhead and must
// justify itself against the Do-Not-Optimize Gate in docs/performance_lab.md.
func BenchmarkNullLane_06_HTTPSecuredChain(b *testing.B) {
	manager, token := testJWT(b)
	authorizer := security.NewAuthorizer([]security.RoleTemplate{{
		Role:         "admin",
		Capabilities: []string{nullLaneEventType},
	}})
	handler := security.SecurityHeaders(
		security.InputValidation(
			security.JWTAuth(manager, nil)(
				security.RequireCapabilities(authorizer, nullLaneEventType)(
					httpapi.CorrelationMiddleware(
						httpapi.NewEventRouteHandler(nullRoute(), nullExecutor),
					),
				),
			),
		),
	)
	body := newNullBody([]byte(`{}`))
	req := nullRequest(b, body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Correlation-ID", "corr-null")
	w := newNullResponseWriter()

	b.ReportAllocs()

	for b.Loop() {
		body.reset()
		handler.ServeHTTP(w, req)
	}
}

// -- Ladder evidence ----------------------------------------------------------

// BenchmarkNullLane_07_JSONObjectMaterialization measures materializing an
// empty JSON object into the dynamic extension container. Transport-ladder
// rule 1 says same-process dispatch must not reach for JSON; this benchmark is
// the evidence behind that rule, and tooling/scripts/transport_ladder_check.sh
// is its enforcement. Compare against rung 01, which moves the same empty
// payload with no materialization at all.
func BenchmarkNullLane_07_JSONObjectMaterialization(b *testing.B) {
	raw := []byte(`{}`)

	b.ReportAllocs()

	for b.Loop() {
		obj, err := extension.ObjectFromJSON(raw)
		if err != nil {
			b.Fatal(err)
		}
		if obj == nil {
			b.Fatal("nil object")
		}
	}
}
