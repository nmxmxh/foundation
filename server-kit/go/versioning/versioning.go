// Package versioning provides HTTP API versioning strategies including header-based
// and path-based versioning with deprecation support.
//
// Usage:
//
//	v := versioning.New(versioning.Config{
//	    Strategy: versioning.StrategyHeader,
//	    HeaderName: "X-API-Version",
//	    DefaultVersion: "v1",
//	})
//
//	mux := http.NewServeMux()
//	mux.Handle("/api/", v.Middleware(router))
//
//	// Or use path-based versioning
//	v.HandleVersion("v1", v1Handler)
//	v.HandleVersion("v2", v2Handler)
package versioning

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Strategy defines the API versioning strategy.
type Strategy string

const (
	// StrategyHeader uses a custom header for versioning.
	StrategyHeader Strategy = "header"
	// StrategyPath uses URL path prefix for versioning.
	StrategyPath Strategy = "path"
	// StrategyQuery uses a query parameter for versioning.
	StrategyQuery Strategy = "query"
	// StrategyAccept uses the Accept header with vendor media type.
	StrategyAccept Strategy = "accept"
)

// Config holds versioning configuration.
type Config struct {
	// Strategy is the versioning strategy to use.
	Strategy Strategy

	// DefaultVersion is used when no version is specified.
	DefaultVersion string

	// SupportedVersions lists all supported API versions.
	SupportedVersions []string

	// HeaderName is the header name for header-based versioning.
	// Default: "X-API-Version"
	HeaderName string

	// QueryParam is the query parameter for query-based versioning.
	// Default: "version"
	QueryParam string

	// VendorName is the vendor name for Accept header versioning.
	// Format: application/vnd.{vendor}.{version}+json
	VendorName string

	// OnVersionMismatch is called when a requested version is not supported.
	OnVersionMismatch func(requested string, supported []string)

	// DeprecationHandler is called when a deprecated version is used.
	DeprecationHandler func(version string, deprecatedAt time.Time, sunsetAt *time.Time)
}

// Version represents an API version with metadata.
type Version struct {
	Name         string     `json:"name"`
	Status       Status     `json:"status"`
	DeprecatedAt *time.Time `json:"deprecated_at,omitempty"`
	SunsetAt     *time.Time `json:"sunset_at,omitempty"`
	Description  string     `json:"description,omitempty"`
}

// Status represents the status of an API version.
type Status string

const (
	StatusCurrent    Status = "current"
	StatusSupported  Status = "supported"
	StatusDeprecated Status = "deprecated"
	StatusSunset     Status = "sunset"
)

// Versioner manages API versioning.
type Versioner struct {
	config   Config
	versions map[string]*Version
	handlers map[string]http.Handler
	mu       sync.RWMutex
}

// New creates a new versioner.
func New(cfg Config) *Versioner {
	if cfg.HeaderName == "" {
		cfg.HeaderName = "X-API-Version"
	}
	if cfg.QueryParam == "" {
		cfg.QueryParam = "version"
	}
	if cfg.DefaultVersion == "" {
		cfg.DefaultVersion = "v1"
	}

	v := &Versioner{
		config:   cfg,
		versions: make(map[string]*Version),
		handlers: make(map[string]http.Handler),
	}

	// Initialize supported versions
	for _, ver := range cfg.SupportedVersions {
		v.versions[ver] = &Version{
			Name:   ver,
			Status: StatusSupported,
		}
	}

	// Mark default as current
	if v.versions[cfg.DefaultVersion] != nil {
		v.versions[cfg.DefaultVersion].Status = StatusCurrent
	}

	return v
}

// RegisterVersion registers a version with metadata.
func (v *Versioner) RegisterVersion(ver Version) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.versions[ver.Name] = &ver
}

// DeprecateVersion marks a version as deprecated.
func (v *Versioner) DeprecateVersion(name string, sunsetAt *time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if ver, ok := v.versions[name]; ok {
		now := time.Now()
		ver.Status = StatusDeprecated
		ver.DeprecatedAt = &now
		ver.SunsetAt = sunsetAt
	}
}

// HandleVersion registers a handler for a specific version.
func (v *Versioner) HandleVersion(version string, handler http.Handler) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.handlers[version] = handler

	if _, ok := v.versions[version]; !ok {
		v.versions[version] = &Version{
			Name:   version,
			Status: StatusSupported,
		}
	}
}

// ExtractVersion extracts the API version from a request.
func (v *Versioner) ExtractVersion(r *http.Request) string {
	switch v.config.Strategy {
	case StrategyHeader:
		return r.Header.Get(v.config.HeaderName)

	case StrategyPath:
		return extractPathVersion(r.URL.Path)

	case StrategyQuery:
		return r.URL.Query().Get(v.config.QueryParam)

	case StrategyAccept:
		return extractAcceptVersion(r.Header.Get("Accept"), v.config.VendorName)

	default:
		return ""
	}
}

// extractPathVersion extracts version from URL path (e.g., /v1/users -> v1).
func extractPathVersion(path string) string {
	re := regexp.MustCompile(`^/?(v\d+(?:\.\d+)?)/`)
	matches := re.FindStringSubmatch(path)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// extractAcceptVersion extracts version from Accept header.
// Format: application/vnd.{vendor}.{version}+json
func extractAcceptVersion(accept, vendor string) string {
	if vendor == "" {
		return ""
	}
	re := regexp.MustCompile(`application/vnd\.` + regexp.QuoteMeta(vendor) + `\.([^+]+)\+json`)
	matches := re.FindStringSubmatch(accept)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

// Middleware returns an HTTP middleware that handles versioning.
func (v *Versioner) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := v.ExtractVersion(r)
		if version == "" {
			version = v.config.DefaultVersion
		}

		// Check if version is supported
		v.mu.RLock()
		ver, ok := v.versions[version]
		v.mu.RUnlock()

		if !ok {
			if v.config.OnVersionMismatch != nil {
				v.config.OnVersionMismatch(version, v.SupportedVersions())
			}
			http.Error(w, fmt.Sprintf("API version '%s' is not supported", version), http.StatusBadRequest)
			return
		}

		// Check for deprecation
		if ver.Status == StatusDeprecated || ver.Status == StatusSunset {
			// Add deprecation headers
			w.Header().Set("Deprecation", "true")
			if ver.DeprecatedAt != nil {
				w.Header().Set("X-Deprecated-At", ver.DeprecatedAt.Format(time.RFC3339))
			}
			if ver.SunsetAt != nil {
				w.Header().Set("Sunset", ver.SunsetAt.Format(time.RFC3339))
			}

			if v.config.DeprecationHandler != nil && ver.DeprecatedAt != nil {
				v.config.DeprecationHandler(version, *ver.DeprecatedAt, ver.SunsetAt)
			}

			// Block sunset versions
			if ver.Status == StatusSunset {
				http.Error(w, fmt.Sprintf("API version '%s' has been sunset", version), http.StatusGone)
				return
			}
		}

		// Add version to context
		ctx := ContextWithVersion(r.Context(), version)
		r = r.WithContext(ctx)

		// Add version header to response
		w.Header().Set("X-API-Version", version)

		next.ServeHTTP(w, r)
	})
}

// Router returns an HTTP handler that routes to version-specific handlers.
func (v *Versioner) Router() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := VersionFromContext(r.Context())
		if version == "" {
			version = v.ExtractVersion(r)
		}
		if version == "" {
			version = v.config.DefaultVersion
		}

		v.mu.RLock()
		handler, ok := v.handlers[version]
		v.mu.RUnlock()

		if !ok {
			http.Error(w, fmt.Sprintf("No handler for version '%s'", version), http.StatusNotFound)
			return
		}

		// Strip version prefix from path for path-based versioning
		if v.config.Strategy == StrategyPath {
			r.URL.Path = stripVersionPrefix(r.URL.Path)
		}

		handler.ServeHTTP(w, r)
	})
}

// stripVersionPrefix removes the version prefix from a path.
func stripVersionPrefix(path string) string {
	re := regexp.MustCompile(`^/?(v\d+(?:\.\d+)?)/`)
	return "/" + re.ReplaceAllString(strings.TrimPrefix(path, "/"), "")
}

// SupportedVersions returns a list of supported version names.
func (v *Versioner) SupportedVersions() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()

	versions := make([]string, 0, len(v.versions))
	for name, ver := range v.versions {
		if ver.Status != StatusSunset {
			versions = append(versions, name)
		}
	}
	sort.Strings(versions)
	return versions
}

// AllVersions returns all versions with their metadata.
func (v *Versioner) AllVersions() []Version {
	v.mu.RLock()
	defer v.mu.RUnlock()

	versions := make([]Version, 0, len(v.versions))
	for _, ver := range v.versions {
		versions = append(versions, *ver)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Name < versions[j].Name
	})
	return versions
}

// VersionsHandler returns an HTTP handler that lists API versions.
func (v *Versioner) VersionsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"versions":        v.AllVersions(),
			"current_version": v.config.DefaultVersion,
		}); err != nil {
			// Log error if needed, but at this point headers are sent
			_ = err
		}
	})
}

// Context key for version
type versionContextKey struct{}

// ContextWithVersion adds the API version to the context.
func ContextWithVersion(ctx context.Context, version string) context.Context {
	return context.WithValue(ctx, versionContextKey{}, version)
}

// VersionFromContext retrieves the API version from the context.
func VersionFromContext(ctx context.Context) string {
	if v := ctx.Value(versionContextKey{}); v != nil {
		return v.(string)
	}
	return ""
}

// VersionedHandler wraps handlers for multiple versions.
type VersionedHandler struct {
	handlers map[string]http.HandlerFunc
	fallback http.HandlerFunc
}

// NewVersionedHandler creates a new versioned handler.
func NewVersionedHandler() *VersionedHandler {
	return &VersionedHandler{
		handlers: make(map[string]http.HandlerFunc),
	}
}

// Handle registers a handler for a specific version.
func (vh *VersionedHandler) Handle(version string, handler http.HandlerFunc) *VersionedHandler {
	vh.handlers[version] = handler
	return vh
}

// Fallback sets a fallback handler.
func (vh *VersionedHandler) Fallback(handler http.HandlerFunc) *VersionedHandler {
	vh.fallback = handler
	return vh
}

// ServeHTTP implements http.Handler.
func (vh *VersionedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	version := VersionFromContext(r.Context())
	if handler, ok := vh.handlers[version]; ok {
		handler(w, r)
		return
	}
	if vh.fallback != nil {
		vh.fallback(w, r)
		return
	}
	http.Error(w, "Version not supported", http.StatusNotFound)
}

// ParseVersion parses a version string (e.g., "v1", "v2.1") into components.
func ParseVersion(v string) (major, minor int, ok bool) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")

	if len(parts) >= 1 {
		if _, err := fmt.Sscanf(parts[0], "%d", &major); err != nil {
			return 0, 0, false
		}
	}
	if len(parts) >= 2 {
		if _, err := fmt.Sscanf(parts[1], "%d", &minor); err != nil {
			return major, 0, true
		}
	}

	return major, minor, true
}

// CompareVersions compares two versions.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func CompareVersions(a, b string) int {
	aMajor, aMinor, _ := ParseVersion(a)
	bMajor, bMinor, _ := ParseVersion(b)

	if aMajor != bMajor {
		if aMajor < bMajor {
			return -1
		}
		return 1
	}

	if aMinor != bMinor {
		if aMinor < bMinor {
			return -1
		}
		return 1
	}

	return 0
}
