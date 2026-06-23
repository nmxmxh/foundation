package projectiongw

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/security"
)

// TestSecurityTenantFunc proves projection access is identity-scoped: the tenant
// is the authenticated organization from request context, and a request without
// one is rejected rather than defaulted.
func TestSecurityTenantFunc(t *testing.T) {
	t.Run("authenticated organization resolves the tenant", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/projections/signals/ticks", nil)
		req = req.WithContext(security.ContextWithOrganizationID(req.Context(), "org_1"))
		tenant, err := SecurityTenantFunc(req)
		if err != nil || tenant != "org_1" {
			t.Fatalf("SecurityTenantFunc() = %q, %v; want org_1, nil", tenant, err)
		}
	})

	t.Run("no identity is rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/v1/projections/signals/ticks", nil)
		if _, err := SecurityTenantFunc(req); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("SecurityTenantFunc() err = %v; want ErrUnauthenticated", err)
		}
	})
}
