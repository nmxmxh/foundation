// Package tracing provides distributed tracing with OpenTelemetry integration.
// It chains with the existing correlationId pattern while adding full OTEL support.
//
// Usage:
//
//	tp, err := tracing.NewProvider(tracing.Config{
//	    ServiceName: "my-service",
//	    Environment: "production",
//	    Endpoint:    "localhost:4317",
//	})
//	defer tp.Shutdown(context.Background())
//
//	ctx, span := tracing.Start(ctx, "operation-name")
//	defer span.End()
package tracing

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// Config holds configuration for the tracing provider.
type Config struct {
	// ServiceName is the name of the service being traced.
	ServiceName string

	// ServiceVersion is the version of the service.
	ServiceVersion string

	// Environment is the deployment environment (development, staging, production).
	Environment string

	// Endpoint is the OTLP collector endpoint (e.g., "localhost:4317").
	// If empty, traces are written to stdout.
	Endpoint string

	// SampleRate is the sampling rate (0.0 to 1.0). Default: 1.0
	SampleRate float64

	// Insecure disables TLS for the OTLP connection.
	Insecure bool

	// Headers are additional headers to send with OTLP requests.
	Headers map[string]string

	// Attributes are additional resource attributes.
	Attributes map[string]string
}

// Provider wraps the OpenTelemetry TracerProvider.
type Provider struct {
	tp     *sdktrace.TracerProvider
	tracer trace.Tracer
}

// NewProvider creates a new tracing provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 1.0
	}

	// Build resource attributes
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	if cfg.Environment != "" {
		attrs = append(attrs, attribute.String("deployment.environment", cfg.Environment))
	}
	for k, v := range cfg.Attributes {
		attrs = append(attrs, attribute.String(k, v))
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes("", attrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create exporter
	var exporter sdktrace.SpanExporter
	if cfg.Endpoint != "" {
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		for k, v := range cfg.Headers {
			opts = append(opts, otlptracegrpc.WithHeaders(map[string]string{k: v}))
		}

		exp, err := otlptracegrpc.New(context.Background(), opts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
		}
		exporter = exp
	} else {
		// Fallback to stdout
		exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, fmt.Errorf("failed to create stdout exporter: %w", err)
		}
		exporter = exp
	}

	// Create sampler
	var sampler sdktrace.Sampler
	if cfg.SampleRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if cfg.SampleRate <= 0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(cfg.SampleRate)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Provider{
		tp:     tp,
		tracer: tp.Tracer(cfg.ServiceName),
	}, nil
}

// Shutdown shuts down the tracing provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	return p.tp.Shutdown(ctx)
}

// Tracer returns the underlying tracer.
func (p *Provider) Tracer() trace.Tracer {
	return p.tracer
}

// Start starts a new span with the given name.
func Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return otel.Tracer("").Start(ctx, name, opts...)
}

// StartWithTracer starts a new span using a specific tracer.
func (p *Provider) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, name, opts...)
}

// SpanFromContext returns the current span from context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// ContextWithSpan returns a new context with the given span.
func ContextWithSpan(ctx context.Context, span trace.Span) context.Context {
	return trace.ContextWithSpan(ctx, span)
}

// SetError records an error on the current span.
func SetError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// SetAttribute sets an attribute on the current span.
func SetAttribute(ctx context.Context, key string, value any) {
	span := trace.SpanFromContext(ctx)
	switch v := value.(type) {
	case string:
		span.SetAttributes(attribute.String(key, v))
	case int:
		span.SetAttributes(attribute.Int(key, v))
	case int64:
		span.SetAttributes(attribute.Int64(key, v))
	case float64:
		span.SetAttributes(attribute.Float64(key, v))
	case bool:
		span.SetAttributes(attribute.Bool(key, v))
	case []string:
		span.SetAttributes(attribute.StringSlice(key, v))
	default:
		span.SetAttributes(attribute.String(key, fmt.Sprintf("%v", v)))
	}
}

// AddEvent adds an event to the current span.
func AddEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

// CorrelationID context key for bridging with existing correlation system.
type correlationIDKey struct{}

// WithCorrelationID adds a correlation ID to the context and span.
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	ctx = context.WithValue(ctx, correlationIDKey{}, correlationID)
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("correlation.id", correlationID))
	return ctx
}

// CorrelationIDFromContext retrieves the correlation ID from context.
func CorrelationIDFromContext(ctx context.Context) string {
	if v := ctx.Value(correlationIDKey{}); v != nil {
		return v.(string)
	}
	return ""
}

// TraceIDFromContext returns the trace ID from the current span.
func TraceIDFromContext(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().HasTraceID() {
		return span.SpanContext().TraceID().String()
	}
	return ""
}

// SpanIDFromContext returns the span ID from the current span.
func SpanIDFromContext(ctx context.Context) string {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().HasSpanID() {
		return span.SpanContext().SpanID().String()
	}
	return ""
}

// Middleware returns an HTTP middleware that starts a span for each request.
func Middleware(serviceName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract trace context from incoming request
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			// Start a new span
			ctx, span := otel.Tracer(serviceName).Start(ctx, r.Method+" "+r.URL.Path,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.request.method", r.Method),
					attribute.String("url.path", r.URL.Path),
					attribute.String("url.scheme", r.URL.Scheme),
					attribute.String("server.address", r.Host),
					attribute.String("user_agent.original", r.UserAgent()),
				),
			)
			defer span.End()

			// Bridge correlation ID if present
			if correlationID := r.Header.Get("X-Correlation-ID"); correlationID != "" {
				ctx = WithCorrelationID(ctx, correlationID)
			}

			// Wrap response writer to capture status code
			wrapped := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r.WithContext(ctx))

			// Record response status
			span.SetAttributes(attribute.Int("http.response.status_code", wrapped.statusCode))
			if wrapped.statusCode >= 400 {
				span.SetStatus(codes.Error, http.StatusText(wrapped.statusCode))
			}
		})
	}
}

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// InjectContext injects trace context into outgoing HTTP request headers.
func InjectContext(ctx context.Context, req *http.Request) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
}

// SpanOptions provides common span options.
type SpanOptions struct {
	// Kind is the span kind (client, server, producer, consumer, internal).
	Kind trace.SpanKind
	// Attributes are additional span attributes.
	Attributes []attribute.KeyValue
}

// StartSpan starts a span with common options.
func StartSpan(ctx context.Context, name string, opts SpanOptions) (context.Context, trace.Span) {
	spanOpts := []trace.SpanStartOption{
		trace.WithSpanKind(opts.Kind),
	}
	if len(opts.Attributes) > 0 {
		spanOpts = append(spanOpts, trace.WithAttributes(opts.Attributes...))
	}
	return Start(ctx, name, spanOpts...)
}

// StartClientSpan starts a client span (for outgoing calls).
func StartClientSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return StartSpan(ctx, name, SpanOptions{
		Kind:       trace.SpanKindClient,
		Attributes: attrs,
	})
}

// StartInternalSpan starts an internal span.
func StartInternalSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return StartSpan(ctx, name, SpanOptions{
		Kind:       trace.SpanKindInternal,
		Attributes: attrs,
	})
}

// Timed executes a function and records its duration as a span.
func Timed(ctx context.Context, name string, fn func(context.Context) error) error {
	ctx, span := Start(ctx, name)
	defer span.End()

	start := time.Now()
	err := fn(ctx)
	duration := time.Since(start)

	span.SetAttributes(attribute.Int64("duration_ms", duration.Milliseconds()))
	if err != nil {
		SetError(ctx, err)
	}

	return err
}
