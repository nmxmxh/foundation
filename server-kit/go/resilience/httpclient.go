package resilience

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/errors"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// HTTPClient is a resilient HTTP client with circuit breaker, retry, and tracing.
type HTTPClient struct {
	runtime    *Runtime
	httpClient *http.Client
	name       string
	baseURL    string
	maxBody    int64
	urlPolicy  *security.OutboundURLPolicy
}

// HTTPClientConfig configures the HTTP client.
type HTTPClientConfig struct {
	Name              string
	BaseURL           string
	Timeout           time.Duration
	MaxIdleConns      int
	IdleConnTimeout   time.Duration
	MaxResponseBytes  int64
	OutboundURLPolicy *security.OutboundURLPolicy
}

// NewHTTPClient creates a new resilient HTTP client.
func NewHTTPClient(runtime *Runtime, cfg HTTPClientConfig) *HTTPClient {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = 10
	}
	if cfg.IdleConnTimeout == 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 10 * 1024 * 1024
	}

	transport := &http.Transport{
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConns,
		IdleConnTimeout:     cfg.IdleConnTimeout,
	}

	client := &http.Client{
		Timeout:   cfg.Timeout,
		Transport: transport,
	}

	return &HTTPClient{
		runtime:    runtime,
		httpClient: client,
		name:       cfg.Name,
		baseURL:    cfg.BaseURL,
		maxBody:    cfg.MaxResponseBytes,
		urlPolicy:  cfg.OutboundURLPolicy,
	}
}

// Request represents an HTTP request to be made.
type Request struct {
	Method  string
	Path    string
	Headers map[string]string
	Body    any
	Query   map[string]string
}

// Response represents an HTTP response.
type Response struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// Do executes an HTTP request with resilience patterns.
func (c *HTTPClient) Do(ctx context.Context, req Request) (*Response, error) {
	// Start tracing span
	ctx, endSpan := c.runtime.StartSpan(ctx, fmt.Sprintf("http.%s.%s", c.name, req.Method))
	defer endSpan()

	// Check if degraded
	if c.runtime.IsDegraded(c.name) {
		return nil, errors.Unavailable(fmt.Sprintf("%s is currently unavailable", c.name))
	}

	var resp *Response
	var lastErr error

	// Execute with circuit breaker and retry
	err := c.runtime.Execute(ctx, c.name, func() error {
		r, err := c.doRequest(ctx, req)
		if err != nil {
			lastErr = err
			return err
		}
		resp = r
		return nil
	})

	if err != nil {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, err
	}

	return resp, nil
}

func (c *HTTPClient) doRequest(ctx context.Context, req Request) (*Response, error) {
	requestURL, err := c.requestURL(ctx, req)
	if err != nil {
		return nil, err
	}

	// Marshal body if present
	var bodyReader io.Reader
	if req.Body != nil {
		bodyBytes, err := json.Marshal(req.Body)
		if err != nil {
			return nil, errors.Internal("failed to marshal request body").WithCause(err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, requestURL, bodyReader)
	if err != nil {
		return nil, errors.Internal("failed to create request").WithCause(err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// Execute request
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Check if it's a timeout
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.Timeout(fmt.Sprintf("%s request timed out", c.name))
		}
		return nil, errors.Unavailable(fmt.Sprintf("%s request failed", c.name)).WithCause(err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	// Read response body
	body, err := readBoundedBody(httpResp.Body, c.maxBody)
	if err != nil {
		return nil, err
	}

	resp := &Response{
		StatusCode: httpResp.StatusCode,
		Headers:    httpResp.Header,
		Body:       body,
	}

	// Check for error status codes
	if httpResp.StatusCode >= 500 {
		// Server error - transient, should retry
		return resp, errors.Unavailable(fmt.Sprintf("%s returned %d", c.name, httpResp.StatusCode))
	}
	if httpResp.StatusCode == 429 {
		// Rate limited
		return resp, errors.RateLimited(fmt.Sprintf("%s rate limited", c.name))
	}
	if httpResp.StatusCode >= 400 {
		// Client error - permanent, should not retry
		return resp, nil // Return response for caller to handle
	}

	return resp, nil
}

func (c *HTTPClient) requestURL(ctx context.Context, req Request) (string, error) {
	base, err := url.Parse(strings.TrimRight(c.baseURL, "/") + "/")
	if err != nil {
		return "", errors.Internal("invalid base url").WithCause(err)
	}
	relative, err := url.Parse(strings.TrimLeft(req.Path, "/"))
	if err != nil {
		return "", errors.Internal("invalid request path").WithCause(err)
	}
	target := base.ResolveReference(relative)
	query := target.Query()
	for key, value := range req.Query {
		query.Set(key, value)
	}
	target.RawQuery = query.Encode()
	if c.urlPolicy != nil {
		if _, err := security.ValidateOutboundURL(ctx, target.String(), *c.urlPolicy); err != nil {
			return "", errors.Validation("unsafe outbound url").WithCause(err)
		}
	}
	return target.String(), nil
}

func readBoundedBody(body io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	limited := io.LimitReader(body, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, errors.Internal("failed to read response body").WithCause(err)
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.Validation("response body too large")
	}
	return data, nil
}

// Get performs a GET request.
func (c *HTTPClient) Get(ctx context.Context, path string, headers map[string]string) (*Response, error) {
	return c.Do(ctx, Request{
		Method:  http.MethodGet,
		Path:    path,
		Headers: headers,
	})
}

// Post performs a POST request.
func (c *HTTPClient) Post(ctx context.Context, path string, body any, headers map[string]string) (*Response, error) {
	return c.Do(ctx, Request{
		Method:  http.MethodPost,
		Path:    path,
		Body:    body,
		Headers: headers,
	})
}

// Put performs a PUT request.
func (c *HTTPClient) Put(ctx context.Context, path string, body any, headers map[string]string) (*Response, error) {
	return c.Do(ctx, Request{
		Method:  http.MethodPut,
		Path:    path,
		Body:    body,
		Headers: headers,
	})
}

// Delete performs a DELETE request.
func (c *HTTPClient) Delete(ctx context.Context, path string, headers map[string]string) (*Response, error) {
	return c.Do(ctx, Request{
		Method:  http.MethodDelete,
		Path:    path,
		Headers: headers,
	})
}

// DecodeJSON decodes the response body as JSON.
func (r *Response) DecodeJSON(dest any) error {
	if err := json.Unmarshal(r.Body, dest); err != nil {
		return errors.Internal("failed to decode response").WithCause(err)
	}
	return nil
}

// IsSuccess returns true if the response status code indicates success.
func (r *Response) IsSuccess() bool {
	return r.StatusCode >= 200 && r.StatusCode < 300
}

// IsClientError returns true if the response indicates a client error.
func (r *Response) IsClientError() bool {
	return r.StatusCode >= 400 && r.StatusCode < 500
}

// IsServerError returns true if the response indicates a server error.
func (r *Response) IsServerError() bool {
	return r.StatusCode >= 500
}
