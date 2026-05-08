package security

import (
	"context"
	"testing"
)

func TestAuthorizerRoleAndDirectCapabilities(t *testing.T) {
	defaultAuthorizer := NewAuthorizer(nil)
	if !defaultAuthorizer.Can("admin", "anything.delete", nil) {
		t.Fatalf("default admin role should grant wildcard")
	}
	if defaultAuthorizer.Can("", "orders.read", nil) || defaultAuthorizer.Can("admin", "", nil) {
		t.Fatalf("invalid role/capability inputs should not grant access")
	}
	authorizer := NewAuthorizer([]RoleTemplate{
		{Role: " ", Capabilities: []string{"ignored"}},
		{Role: "operator", Capabilities: []string{"orders.view", "inventory.*", " "}},
	})

	if !authorizer.CanAccess("operator", "orders.list", PermissionView, nil) {
		t.Fatalf("orders.view should grant view on orders domain")
	}
	if authorizer.CanAccess("operator", "orders.refund", PermissionWrite, nil) {
		t.Fatalf("orders.view should not grant write")
	}
	if !authorizer.CanAccess("operator", "billing.refund", PermissionWrite, []string{"billing.write"}) {
		t.Fatalf("direct write capability should grant billing write")
	}
	if !authorizer.CanAccess("operator", "inventory.recount", PermissionAdmin, nil) {
		t.Fatalf("inventory.* should grant admin action")
	}
}

func TestAuthorizerRequireFromContext(t *testing.T) {
	authorizer := NewAuthorizer([]RoleTemplate{
		{Role: "analyst", Capabilities: []string{"reports.view"}},
	})
	ctx := ContextWithRole(context.Background(), "analyst")

	if err := authorizer.RequireAccess(ctx, "reports.export", PermissionView); err != nil {
		t.Fatalf("RequireAccess() error = %v", err)
	}
	if err := authorizer.RequireAccess(ctx, "reports.delete", PermissionWrite); err == nil {
		t.Fatalf("expected write rejection")
	}
	if err := authorizer.Require(ctx, "reports.export"); err == nil {
		t.Fatalf("expected exact require rejection for missing direct capability")
	}
	ctx = ContextWithRole(context.Background(), "analyst")
	ctx = ContextWithCapabilities(ctx, []string{"users.invite"})
	if err := authorizer.RequireAny(ctx, "billing.refund", "users.invite"); err != nil {
		t.Fatalf("RequireAny() error = %v", err)
	}
}

func TestEventCapabilityAndPermissionDerivation(t *testing.T) {
	cases := map[string]struct {
		capability string
		permission string
	}{
		"orders:list_orders:requested":   {"orders.list_orders", PermissionView},
		"orders:admin_suspend:success":   {"orders.admin_suspend", PermissionAdmin},
		"orders:update_status:requested": {"orders.update_status", PermissionWrite},
		"":                               {"", PermissionWrite},
	}
	for eventType, want := range cases {
		if got := CapabilityFromEvent(eventType); got != want.capability {
			t.Fatalf("CapabilityFromEvent(%q) = %q, want %q", eventType, got, want.capability)
		}
		if got := PermissionFromEvent(eventType); got != want.permission {
			t.Fatalf("PermissionFromEvent(%q) = %q, want %q", eventType, got, want.permission)
		}
	}
	if got := NormalizePermission("unknown"); got != PermissionWrite {
		t.Fatalf("NormalizePermission unknown = %q", got)
	}
}
