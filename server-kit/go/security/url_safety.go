package security

import (
	"context"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var (
	ErrUnsafeURL          = errors.New("unsafe url")
	ErrDuplicateParameter = errors.New("duplicate query parameter")
	ErrUnsafePath         = errors.New("unsafe path")
)

// IPResolver resolves a hostname to IP addresses for outbound URL validation.
type IPResolver func(context.Context, string) ([]net.IP, error)

// OutboundURLPolicy controls SSRF-oriented validation for URLs used by servers.
type OutboundURLPolicy struct {
	AllowedHosts         []string
	AllowedSchemes       []string
	AllowPrivateNetworks bool
	Resolver             IPResolver
	LookupTimeout        time.Duration
}

// ValidateRedirectTarget accepts same-origin relative redirects and exact-match
// absolute redirects. It rejects CRLF/control characters, userinfo, schemeless
// network paths, and suffix-style host tricks.
func ValidateRedirectTarget(raw string, allowedHosts []string) (*url.URL, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" || containsControl(candidate) || containsEscapedControl(candidate) || strings.Contains(candidate, "\\") {
		return nil, ErrUnsafeURL
	}
	if strings.HasPrefix(candidate, "//") {
		return nil, ErrUnsafeURL
	}
	if strings.HasPrefix(candidate, "/") {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Host != "" || parsed.Scheme != "" {
			return nil, ErrUnsafeURL
		}
		return parsed, nil
	}

	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, ErrUnsafeURL
	}
	if !isAllowedScheme(parsed.Scheme, []string{"https", "http"}) || parsed.User != nil {
		return nil, ErrUnsafeURL
	}
	if !hostInAllowlist(parsed.Hostname(), allowedHosts) {
		return nil, ErrUnsafeURL
	}
	return parsed, nil
}

// ValidateOutboundURL vets server-side fetch destinations before a caller opens
// a network connection. Callers that follow redirects must re-run this function
// on each Location value before issuing the next request.
func ValidateOutboundURL(ctx context.Context, raw string, policy OutboundURLPolicy) (*url.URL, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" || containsControl(candidate) || containsEscapedControl(candidate) {
		return nil, ErrUnsafeURL
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return nil, ErrUnsafeURL
	}
	if !isAllowedScheme(parsed.Scheme, defaultSchemes(policy.AllowedSchemes)) {
		return nil, ErrUnsafeURL
	}
	host := parsed.Hostname()
	if len(policy.AllowedHosts) > 0 && !hostInAllowlist(host, policy.AllowedHosts) {
		return nil, ErrUnsafeURL
	}
	if policy.AllowPrivateNetworks {
		return parsed, nil
	}
	ips, err := resolveHost(ctx, host, policy)
	if err != nil || len(ips) == 0 {
		return nil, ErrUnsafeURL
	}
	if slices.ContainsFunc(ips, isPrivateOrLocalIP) {
		return nil, ErrUnsafeURL
	}
	return parsed, nil
}

// RejectDuplicateQueryParams fails closed on HTTP parameter pollution instead
// of relying on first-value or last-value parser behavior.
func RejectDuplicateQueryParams(values url.Values, protectedParams ...string) error {
	protected := map[string]struct{}{}
	for _, param := range protectedParams {
		if key := strings.TrimSpace(param); key != "" {
			protected[key] = struct{}{}
		}
	}
	for key, items := range values {
		if len(items) < 2 {
			continue
		}
		if len(protected) == 0 {
			return ErrDuplicateParameter
		}
		if _, ok := protected[key]; ok {
			return ErrDuplicateParameter
		}
	}
	return nil
}

// SafePathJoin joins an untrusted relative path under root after normalization.
func SafePathJoin(root, unsafePath string) (string, error) {
	base, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil || base == "" {
		return "", ErrUnsafePath
	}
	if unsafePath == "" || filepath.IsAbs(unsafePath) || containsControl(unsafePath) {
		return "", ErrUnsafePath
	}
	target, err := filepath.Abs(filepath.Join(base, filepath.Clean(unsafePath)))
	if err != nil {
		return "", ErrUnsafePath
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrUnsafePath
	}
	return target, nil
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0
}

func containsEscapedControl(value string) bool {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return true
	}
	return decoded != value && containsControl(decoded)
}

func defaultSchemes(schemes []string) []string {
	if len(schemes) == 0 {
		return []string{"https"}
	}
	return schemes
}

func isAllowedScheme(scheme string, allowed []string) bool {
	for _, item := range allowed {
		if strings.EqualFold(scheme, strings.TrimSpace(item)) {
			return true
		}
	}
	return false
}

func hostInAllowlist(host string, allowed []string) bool {
	normalized := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if normalized == "" {
		return false
	}
	for _, item := range allowed {
		candidate := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(item)), ".")
		if candidate != "" && normalized == candidate {
			return true
		}
	}
	return false
}

func resolveHost(ctx context.Context, host string, policy OutboundURLPolicy) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	resolver := policy.Resolver
	if resolver == nil {
		timeout := policy.LookupTimeout
		if timeout <= 0 {
			timeout = 2 * time.Second
		}
		lookupCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
		if err != nil {
			return nil, err
		}
		ips := make([]net.IP, 0, len(addrs))
		for _, addr := range addrs {
			ips = append(ips, addr.IP)
		}
		return ips, nil
	}
	return resolver(ctx, host)
}

func isPrivateOrLocalIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}
