// Package apidocs serves generated OpenAPI specs and a small Swagger UI shell.
package apidocs

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultSpecURL  = "/openapi.json"
	defaultDocsPath = "/docs"
)

// Options configures the API documentation handler.
type Options struct {
	SpecPaths   []string
	SpecURL     string
	DocsPath    string
	Title       string
	Description string
}

// Handler serves an OpenAPI spec and browser documentation for it.
type Handler struct {
	spec        []byte
	specPath    string
	loadErr     error
	specURL     string
	docsPath    string
	title       string
	description string
}

// New loads the first non-empty OpenAPI spec from opts.SpecPaths.
func New(opts Options) *Handler {
	specURL := strings.TrimSpace(opts.SpecURL)
	if specURL == "" {
		specURL = defaultSpecURL
	}
	docsPath := strings.TrimRight(strings.TrimSpace(opts.DocsPath), "/")
	if docsPath == "" {
		docsPath = defaultDocsPath
	}
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = "API Documentation"
	}
	description := strings.TrimSpace(opts.Description)
	if description == "" {
		description = "Interactive OpenAPI documentation for this service."
	}

	spec, specPath, err := LoadSpec(opts.SpecPaths)
	if len(spec) > 0 {
		title, description = specInfo(spec, title, description)
	}

	return &Handler{
		spec:        spec,
		specPath:    specPath,
		loadErr:     err,
		specURL:     specURL,
		docsPath:    docsPath,
		title:       title,
		description: description,
	}
}

// DefaultSpecPaths returns the paths used by scaffolded local and Docker runs.
func DefaultSpecPaths() []string {
	return []string{
		"openapi.json",
		"/srv/openapi.json",
		"/openapi.json",
	}
}

// PublicPaths returns documentation paths that should bypass JWT middleware.
func PublicPaths() []string {
	return []string{defaultSpecURL, defaultDocsPath}
}

// LoadSpec returns the first non-empty spec found in candidatePaths.
func LoadSpec(candidatePaths []string) ([]byte, string, error) {
	if len(candidatePaths) == 0 {
		candidatePaths = DefaultSpecPaths()
	}
	var lastErr error
	for _, candidate := range candidatePaths {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		spec, err := readSpecCandidate(candidate)
		if err == nil && len(spec) > 0 {
			return spec, candidate, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("openapi spec not found in %v", candidatePaths)
	}
	return nil, "", lastErr
}

func readSpecCandidate(candidate string) ([]byte, error) {
	clean := filepath.Clean(candidate)
	if filepath.IsAbs(clean) {
		return readSpecFromRoot(filepath.Dir(clean), filepath.Base(clean))
	}
	return readSpecFromRoot(".", clean)
}

func readSpecFromRoot(rootDir, name string) ([]byte, error) {
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(name)
}

// Loaded reports whether a non-empty spec was found.
func (h *Handler) Loaded() bool {
	return h != nil && len(h.spec) > 0
}

// SpecPath returns the path used to load the spec.
func (h *Handler) SpecPath() string {
	if h == nil {
		return ""
	}
	return h.specPath
}

// LoadError returns the spec load error, if any.
func (h *Handler) LoadError() error {
	if h == nil {
		return nil
	}
	return h.loadErr
}

// Register installs /openapi.json, /docs, and /docs/* on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	if h == nil || mux == nil {
		return
	}
	mux.HandleFunc(h.specURL, h.ServeSpec)
	mux.HandleFunc(h.docsPath, h.ServeDocs)
	mux.HandleFunc(h.docsPath+"/", h.RedirectToDocs)
}

// ServeSpec serves the OpenAPI JSON document.
func (h *Handler) ServeSpec(w http.ResponseWriter, r *http.Request) {
	if !allowGetHead(w, r) {
		return
	}
	if h == nil || len(h.spec) == 0 {
		http.Error(w, "OpenAPI spec not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(h.spec)
}

// ServeDocs serves a compact Swagger UI shell for the generated spec.
func (h *Handler) ServeDocs(w http.ResponseWriter, r *http.Request) {
	if !allowGetHead(w, r) {
		return
	}
	if h == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline' https://unpkg.com; script-src 'self' 'unsafe-inline' https://unpkg.com; font-src 'self' data: https://unpkg.com; connect-src 'self' https: ws: wss:")
	_, _ = w.Write([]byte(h.html()))
}

// RedirectToDocs keeps nested Swagger asset-like paths on the canonical page.
func (h *Handler) RedirectToDocs(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, h.docsPath, http.StatusTemporaryRedirect)
}

// ServeIndex serves the API documentation at the server root. HTML clients
// (browsers) get the Swagger UI shell; everything else gets the raw OpenAPI
// spec — the same content negotiation an API server landing page is expected
// to do. Only an exact "/" is handled; any other unmatched path 404s so this
// root catch-all never masks a real route. Registered so the API server root
// shows docs instead of an authorization error.
func (h *Handler) ServeIndex(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !allowGetHead(w, r) {
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		h.ServeDocs(w, r)
		return
	}
	h.ServeSpec(w, r)
}

func allowGetHead(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	return false
}

func specInfo(spec []byte, fallbackTitle, fallbackDescription string) (string, string) {
	var parsed struct {
		Info struct {
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"info"`
	}
	if err := json.Unmarshal(spec, &parsed); err != nil {
		return fallbackTitle, fallbackDescription
	}
	title := strings.TrimSpace(parsed.Info.Title)
	if title == "" {
		title = fallbackTitle
	}
	description := strings.TrimSpace(parsed.Info.Description)
	if description == "" {
		description = fallbackDescription
	}
	return title, description
}

func (h *Handler) html() string {
	title := html.EscapeString(h.title)
	description := html.EscapeString(h.description)
	specURL := html.EscapeString(h.specURL)
	return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>` + title + `</title>
    <meta name="description" content="` + description + `" />
    <link rel="alternate" type="application/json" href="` + specURL + `" title="OpenAPI spec" />
    <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
    <style>
      :root {
        color-scheme: light dark;
        --bg: #f7f4ee;
        --surface: #fffdf8;
        --text: #191815;
        --muted: #696257;
        --border: #ded4c3;
        --accent: #0f766e;
      }
      @media (prefers-color-scheme: dark) {
        :root {
          --bg: #151513;
          --surface: #20201d;
          --text: #f7f4ee;
          --muted: #c5bcac;
          --border: #39362f;
          --accent: #5eead4;
        }
      }
      body {
        margin: 0;
        background: var(--bg);
        color: var(--text);
        font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      }
      .api-header {
        display: flex;
        justify-content: space-between;
        gap: 16px;
        align-items: center;
        padding: 16px 24px;
        background: var(--surface);
        border-bottom: 1px solid var(--border);
      }
      .api-title {
        margin: 0;
        font-size: 18px;
        font-weight: 700;
      }
      .api-description {
        margin: 4px 0 0;
        color: var(--muted);
        font-size: 14px;
      }
      .api-link {
        color: var(--accent);
        font-size: 14px;
        font-weight: 600;
        text-decoration: none;
        white-space: nowrap;
      }
      .swagger-ui,
      .swagger-ui input,
      .swagger-ui select,
      .swagger-ui textarea,
      .swagger-ui button {
        font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif !important;
      }
      .swagger-ui .topbar { display: none !important; }
      .swagger-ui .info { margin: 24px 0 16px !important; }
      .swagger-ui .scheme-container {
        background: var(--surface) !important;
        border-bottom: 1px solid var(--border) !important;
        box-shadow: none !important;
      }
      .swagger-ui .opblock {
        border-radius: 4px !important;
        box-shadow: none !important;
      }
    </style>
  </head>
  <body>
    <header class="api-header">
      <div>
        <h1 class="api-title">` + title + `</h1>
        <p class="api-description">` + description + `</p>
      </div>
      <a class="api-link" href="` + specURL + `">OpenAPI JSON</a>
    </header>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-standalone-preset.js"></script>
    <script>
      window.onload = function () {
        window.ui = SwaggerUIBundle({
          url: "` + specURL + `",
          dom_id: "#swagger-ui",
          deepLinking: true,
          presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
          layout: "StandaloneLayout"
        });
      };
    </script>
  </body>
</html>`
}
