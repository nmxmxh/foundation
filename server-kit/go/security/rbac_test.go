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
	if authorizer.CanAccess("operator", "*", "", nil) {
		t.Fatalf("operator should not be granted wildcard capability")
	}
	if authorizer.CanAccess("guest", "*", "", nil) {
		t.Fatalf("guest should not be granted wildcard capability")
	}
	if !defaultAuthorizer.CanAccess("admin", "*", "", nil) {
		t.Fatalf("admin with wildcard granted should be granted wildcard capability")
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

func TestAuthorizerCapabilityMask(t *testing.T) {
	authorizer := NewAuthorizer([]RoleTemplate{
		{Role: "admin", Capabilities: []string{"*"}},
		{Role: "editor", Capabilities: []string{"article:edit:v1", "article:create:v1"}},
		{Role: "viewer", Capabilities: []string{"article:read:v1"}},
	})

	editBit, okEdit := authorizer.CapabilityBit("article:edit:v1")
	if !okEdit || editBit == CapabilityMaskNone {
		t.Fatalf("expected indexed capability bit for article:edit:v1")
	}

	readBit, okRead := authorizer.CapabilityBit("article:read:v1")
	if !okRead || readBit == CapabilityMaskNone {
		t.Fatalf("expected indexed capability bit for article:read:v1")
	}

	if editBit == readBit {
		t.Fatalf("different capabilities must have distinct bits")
	}

	adminMask := authorizer.RoleMask("admin")
	if adminMask != CapabilityMaskAll {
		t.Fatalf("admin role must have CapabilityMaskAll, got %x", adminMask)
	}

	editorMask := authorizer.RoleMask("editor")
	if !CheckMask(editorMask, editBit) {
		t.Fatalf("editor mask must contain editBit")
	}
	if CheckMask(editorMask, readBit) {
		t.Fatalf("editor mask must NOT contain readBit")
	}

	// Verify CheckMask logic: Authorized = (ActorMask & RequiredMask) == RequiredMask
	combinedReq, okReq := authorizer.CapabilityMaskFor("article:edit:v1", "article:create:v1")
	if !okReq || !CheckMask(editorMask, combinedReq) {
		t.Fatalf("editor mask must satisfy combined edit+create mask")
	}

	// Test fast path through Can()
	if !authorizer.Can("editor", "article:edit:v1", nil) {
		t.Fatalf("Can() fast-path failed for editor on article:edit:v1")
	}
	if authorizer.Can("viewer", "article:edit:v1", nil) {
		t.Fatalf("Can() fast-path incorrectly granted edit to viewer")
	}

	// Dynamic registration
	newBit, okNew := authorizer.RegisterCapability("article:publish:v1")
	if !okNew || newBit == CapabilityMaskNone {
		t.Fatalf("expected RegisterCapability to succeed, got %x", newBit)
	}
	dupBit, okDup := authorizer.RegisterCapability("article:publish:v1")
	if !okDup || dupBit != newBit {
		t.Fatalf("duplicate RegisterCapability must return same bit")
	}

	// Empty and nil guards
	if _, okEmpty := authorizer.RegisterCapability(" "); okEmpty {
		t.Fatal("empty capability registration should return false")
	}
	var nilAuth *Authorizer
	if _, okNil := nilAuth.RegisterCapability("foo"); okNil {
		t.Fatal("nil authorizer registration should return false")
	}
	if bitNil, ok := nilAuth.CapabilityBit("foo"); ok || bitNil != CapabilityMaskNone {
		t.Fatal("nil authorizer CapabilityBit should return 0, false")
	}
	if maskNil := nilAuth.RoleMask("admin"); maskNil != CapabilityMaskNone {
		t.Fatal("nil authorizer RoleMask should return 0")
	}
	if maskNil, ok := nilAuth.CapabilityMaskFor("foo"); ok || maskNil != CapabilityMaskNone {
		t.Fatal("nil authorizer CapabilityMaskFor should return 0, false")
	}
	if maskEmpty, ok := authorizer.CapabilityMaskFor(); ok || maskEmpty != CapabilityMaskNone {
		t.Fatal("empty CapabilityMaskFor should return 0, false")
	}
	if _, okUnk := authorizer.CapabilityMaskFor("unknown:cap:v1"); okUnk {
		t.Fatal("unindexed capability should return false")
	}
}

func BenchmarkAuthorizer_Can_Bitmask(b *testing.B) {
	authorizer := NewAuthorizer([]RoleTemplate{
		{Role: "editor", Capabilities: []string{"article:edit:v1", "article:create:v1"}},
	})
	b.ReportAllocs()

	for b.Loop() {
		if !authorizer.Can("editor", "article:edit:v1", nil) {
			b.Fatal("unauthorized")
		}
	}
}

func BenchmarkAuthorizer_CheckMask_DirectBitwise(b *testing.B) {
	actorMask := CapabilityMask(0b11)
	reqMask := CapabilityMask(0b01)
	b.ReportAllocs()

	for b.Loop() {
		if !CheckMask(actorMask, reqMask) {
			b.Fatal("unauthorized")
		}
	}
}
