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

// nonRoutablePrefixes lists special-purpose ranges that net.IP does not classify
// as private but that an SSRF target must never reach. It covers CGNAT
// (RFC 6598), the benchmarking block (RFC 2544), IETF protocol assignments
// (RFC 6890), TEST-NET documentation ranges (RFC 5737), and the IPv6
// counterparts. Go treats documentation addresses as global unicast, so they
// need an explicit deny.
var nonRoutablePrefixes = buildPrefixes(
	// IPv4 special-purpose.
	"100.64.0.0/10",      // RFC 6598 shared address space (CGNAT).
	"192.0.0.0/24",       // RFC 6890 IETF protocol assignments.
	"192.0.2.0/24",       // RFC 5737 TEST-NET-1.
	"198.18.0.0/15",      // RFC 2544 benchmarking.
	"198.51.100.0/24",    // RFC 5737 TEST-NET-2.
	"203.0.113.0/24",     // RFC 5737 TEST-NET-3.
	"240.0.0.0/4",        // RFC 1112 reserved.
	"255.255.255.255/32", // Limited broadcast.
	// IPv6 special-purpose.
	"2001:db8::/32", // RFC 3849 documentation.
	"2001::/23",     // RFC 6890 IETF protocol assignments.
	"2002::/16",     // 6to4 wrapping a possibly private IPv4.
	"fc00::/7",      // RFC 4193 unique local.
	"fe80::/10",     // RFC 4291 link-local.
)

func buildPrefixes(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, raw := range cidrs {
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			continue
		}
		out = append(out, n)
	}
	return out
}

func isPrivateOrLocalIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Normalize IPv4-mapped IPv6 (::ffff:127.0.0.1) so the embedded IPv4 rules
	// apply. 6to4 and Teredo carry IPv4 in the low bits; check those too.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, n := range nonRoutablePrefixes {
		if n.Contains(ip) {
			return true
		}
	}
	// Teredo (2001::/32) embeds a client IPv4 in the last 32 bits, negated.
	// IPv4-compatible (::a.b.c.d) embeds it directly.
	if embedded := embeddedIPv4(ip); embedded != nil && isPrivateOrLocalIP(embedded) {
		return true
	}
	return false
}

// embeddedIPv4 returns the IPv4 address tunneled inside a Teredo or
// IPv4-compatible IPv6 address, or nil when the address is neither.
func embeddedIPv4(ip net.IP) net.IP {
	v6 := ip.To16()
	if v6 == nil {
		return nil
	}
	switch {
	case v6[0] == 0x20 && v6[1] == 0x01 && v6[2] == 0x00 && v6[3] == 0x00:
		// Teredo: client IPv4 is the last 4 bytes, inverted.
		obfuscated := net.IPv4(v6[12], v6[13], v6[14], v6[15])
		return net.IPv4(^obfuscated[12], ^obfuscated[13], ^obfuscated[14], ^obfuscated[15])
	case isIPv4Compatible(v6):
		return net.IPv4(v6[12], v6[13], v6[14], v6[15])
	default:
		return nil
	}
}

// isIPv4Compatible reports whether v6 is the deprecated ::a.b.c.d form, which
// uses twelve zero bytes. ::1 sets byte 11 and the unspecified address is all
// zero, so both stay excluded.
func isIPv4Compatible(v6 net.IP) bool {
	for i := range 12 {
		if v6[i] != 0 {
			return false
		}
	}
	return v6[12] != 0 || v6[13] != 0 || v6[14] != 0 || v6[15] != 0
}
