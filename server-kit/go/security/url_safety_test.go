package security

import (
	"context"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"testing"
)

func TestValidateRedirectTargetAllowsRelativeAndExactHosts(t *testing.T) {
	relative, err := ValidateRedirectTarget("/workspace?tab=home", []string{"app.example.com"})
	if err != nil {
		t.Fatalf("relative redirect rejected: %v", err)
	}
	if relative.String() != "/workspace?tab=home" {
		t.Fatalf("relative redirect = %q", relative.String())
	}

	absolute, err := ValidateRedirectTarget("https://app.example.com/callback", []string{"app.example.com"})
	if err != nil {
		t.Fatalf("absolute redirect rejected: %v", err)
	}
	if absolute.Hostname() != "app.example.com" {
		t.Fatalf("absolute hostname = %q", absolute.Hostname())
	}
}

func TestValidateRedirectTargetRejectsOpenRedirectTricks(t *testing.T) {
	cases := []string{
		"//evil.example.com/path",
		"https://app.example.com.evil.test/callback",
		"https://app.example.com@evil.test/callback",
		"https://app.example.com/%0d%0aSet-Cookie:x=y\r\n",
		`/safe\evil`,
		"javascript:alert(1)",
	}
	for _, tc := range cases {
		if _, err := ValidateRedirectTarget(tc, []string{"app.example.com"}); !errors.Is(err, ErrUnsafeURL) {
			t.Fatalf("ValidateRedirectTarget(%q) error = %v, want ErrUnsafeURL", tc, err)
		}
	}
}

func TestValidateOutboundURLRejectsSSRFNetworkTargets(t *testing.T) {
	policy := OutboundURLPolicy{
		AllowedSchemes: []string{"https"},
		Resolver: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.0.0.12")}, nil
		},
	}
	if _, err := ValidateOutboundURL(context.Background(), "https://webhook.example.com/push", policy); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("private resolved IP error = %v, want ErrUnsafeURL", err)
	}
	if _, err := ValidateOutboundURL(context.Background(), "https://127.0.0.1/admin", OutboundURLPolicy{}); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("loopback literal error = %v, want ErrUnsafeURL", err)
	}
}

func TestValidateOutboundURLEdgeCases(t *testing.T) {
	if _, err := ValidateOutboundURL(context.Background(), "ftp://api.partner.example/events", OutboundURLPolicy{}); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("unexpected scheme error = %v", err)
	}
	if _, err := ValidateOutboundURL(context.Background(), "https://user:pass@api.partner.example/events", OutboundURLPolicy{}); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("userinfo error = %v", err)
	}
	if _, err := ValidateOutboundURL(context.Background(), "https://api.partner.example/%zz", OutboundURLPolicy{}); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("escaped control parse error = %v", err)
	}
	if _, err := ValidateOutboundURL(context.Background(), "https://evil.example/events", OutboundURLPolicy{
		AllowedHosts: []string{"api.partner.example"},
	}); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("allowlist error = %v", err)
	}
	if _, err := ValidateOutboundURL(context.Background(), "https://api.partner.example/events", OutboundURLPolicy{
		Resolver: func(context.Context, string) ([]net.IP, error) { return nil, errors.New("dns down") },
	}); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("resolver error = %v", err)
	}
	if _, err := ValidateOutboundURL(context.Background(), "https://api.partner.example/events", OutboundURLPolicy{
		Resolver: func(context.Context, string) ([]net.IP, error) { return nil, nil },
	}); !errors.Is(err, ErrUnsafeURL) {
		t.Fatalf("empty resolver result error = %v", err)
	}
	if _, err := ValidateOutboundURL(context.Background(), "https://10.0.0.1/events", OutboundURLPolicy{
		AllowPrivateNetworks: true,
	}); err != nil {
		t.Fatalf("private network override rejected: %v", err)
	}
}

func TestValidateOutboundURLAllowsPublicResolvedHost(t *testing.T) {
	policy := OutboundURLPolicy{
		AllowedHosts: []string{"api.partner.example"},
		Resolver: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.24")}, nil
		},
	}
	parsed, err := ValidateOutboundURL(context.Background(), "https://api.partner.example/events", policy)
	if err != nil {
		t.Fatalf("public URL rejected: %v", err)
	}
	if parsed.Hostname() != "api.partner.example" {
		t.Fatalf("hostname = %q", parsed.Hostname())
	}
}

func TestRejectDuplicateQueryParams(t *testing.T) {
	values := url.Values{
		"role":  []string{"user", "admin"},
		"scope": []string{"read"},
	}
	if err := RejectDuplicateQueryParams(values); !errors.Is(err, ErrDuplicateParameter) {
		t.Fatalf("duplicate query error = %v, want ErrDuplicateParameter", err)
	}
	if err := RejectDuplicateQueryParams(values, "state"); err != nil {
		t.Fatalf("unprotected duplicate rejected: %v", err)
	}
	if err := RejectDuplicateQueryParams(values, "role"); !errors.Is(err, ErrDuplicateParameter) {
		t.Fatalf("protected duplicate error = %v, want ErrDuplicateParameter", err)
	}
}

func TestSafePathJoinRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := SafePathJoin(root, "../secret.txt"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("traversal error = %v, want ErrUnsafePath", err)
	}
	path, err := SafePathJoin(root, "uploads/report.json")
	if err != nil {
		t.Fatalf("safe path rejected: %v", err)
	}
	want := filepath.Join(root, "uploads", "report.json")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestSafePathJoinRejectsAbsoluteAndControlPaths(t *testing.T) {
	root := t.TempDir()
	for _, unsafe := range []string{"", "/etc/passwd", "uploads/\x00secret"} {
		if _, err := SafePathJoin(root, unsafe); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("SafePathJoin(%q) error = %v, want ErrUnsafePath", unsafe, err)
		}
	}
}
