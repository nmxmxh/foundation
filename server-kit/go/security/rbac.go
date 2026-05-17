package security

import (
	"context"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
)

// RoleTemplate defines a reusable capability template.
type RoleTemplate struct {
	Role         string   `json:"role"`
	Capabilities []string `json:"capabilities"`
}

const (
	PermissionView  = "view"
	PermissionWrite = "write"
	PermissionAdmin = "admin"
)

// Authorizer holds role-capability mappings.
type Authorizer struct {
	byRole map[string]map[string]struct{}
}

func NewAuthorizer(templates []RoleTemplate) *Authorizer {
	if len(templates) == 0 {
		templates = DefaultRoleTemplates()
	}
	byRole := map[string]map[string]struct{}{}
	for _, tpl := range templates {
		role := strings.TrimSpace(tpl.Role)
		if role == "" {
			continue
		}
		if _, ok := byRole[role]; !ok {
			byRole[role] = map[string]struct{}{}
		}
		for _, capability := range tpl.Capabilities {
			capability = strings.TrimSpace(capability)
			if capability == "" {
				continue
			}
			byRole[role][capability] = struct{}{}
		}
	}
	return &Authorizer{byRole: byRole}
}

func DefaultRoleTemplates() []RoleTemplate {
	return []RoleTemplate{
		{
			Role: "admin",
			Capabilities: []string{
				"*",
			},
		},
	}
}

func (a *Authorizer) Can(role, capability string, directCapabilities []string) bool {
	return a.CanAccess(role, capability, "", directCapabilities)
}

func (a *Authorizer) CanAccess(role, capability, permission string, directCapabilities []string) bool {
	role = strings.TrimSpace(role)
	capability = strings.TrimSpace(capability)
	permission = NormalizePermission(permission)
	if role == "" || capability == "" {
		return false
	}

	if capability == "*" {
		return true
	}

	for _, granted := range directCapabilities {
		if grantsAccess(granted, capability, permission) {
			return true
		}
	}

	if a == nil {
		return false
	}
	roleCaps, ok := a.byRole[role]
	if !ok {
		return false
	}
	for granted := range roleCaps {
		if grantsAccess(granted, capability, permission) {
			return true
		}
	}
	return false
}

func (a *Authorizer) Require(ctx context.Context, capability string) error {
	return a.RequireAccess(ctx, capability, "")
}

func (a *Authorizer) RequireAccess(ctx context.Context, capability, permission string) error {
	role := GetRoleFromContext(ctx)
	capabilities := GetCapabilitiesFromContext(ctx)
	if !a.CanAccess(role, capability, permission, capabilities) {
		return domainerr.Forbidden("insufficient_capability", "insufficient capability")
	}
	return nil
}

func (a *Authorizer) RequireAny(ctx context.Context, capabilities ...string) error {
	role := GetRoleFromContext(ctx)
	granted := GetCapabilitiesFromContext(ctx)
	for _, capability := range capabilities {
		if a.Can(role, capability, granted) {
			return nil
		}
	}
	return domainerr.Forbidden("insufficient_capability", "insufficient capability")
}

func capabilityGranted(granted, required string) bool {
	granted = strings.TrimSpace(granted)
	required = strings.TrimSpace(required)
	if granted == "" || required == "" {
		return false
	}
	if granted == "*" || granted == required {
		return true
	}
	if before, ok := strings.CutSuffix(granted, ".*"); ok {
		prefix := before
		return strings.HasPrefix(required, prefix+".")
	}
	return false
}

func grantsAccess(granted, capability, permission string) bool {
	if capabilityGranted(granted, capability) {
		return true
	}
	if permission == "" {
		return false
	}

	domain := capabilityDomain(capability)
	if domain == "" {
		return false
	}
	granted = strings.TrimSpace(granted)
	if granted == "*" {
		return true
	}

	switch permission {
	case PermissionView:
		return granted == domain+"."+PermissionView ||
			granted == domain+"."+PermissionWrite ||
			granted == domain+"."+PermissionAdmin
	case PermissionWrite:
		return granted == domain+"."+PermissionWrite ||
			granted == domain+"."+PermissionAdmin
	case PermissionAdmin:
		return granted == domain+"."+PermissionAdmin
	default:
		return false
	}
}

func CapabilityFromEvent(eventType string) string {
	parts := strings.Split(strings.TrimSpace(eventType), ":")
	if len(parts) < 2 {
		return ""
	}
	domain := strings.TrimSpace(parts[0])
	action := strings.TrimSpace(parts[1])
	if domain == "" || action == "" {
		return ""
	}
	return domain + "." + action
}

func PermissionFromEvent(eventType string) string {
	parts := strings.Split(strings.TrimSpace(eventType), ":")
	if len(parts) < 2 {
		return PermissionWrite
	}
	action := strings.ToLower(strings.TrimSpace(parts[1]))
	if action == "" {
		return PermissionWrite
	}
	for _, prefix := range []string{"get_", "list_", "ping", "preview_", "read_"} {
		if strings.HasPrefix(action, prefix) {
			return PermissionView
		}
	}
	for _, prefix := range []string{"admin_", "moderate_", "govern_"} {
		if strings.HasPrefix(action, prefix) {
			return PermissionAdmin
		}
	}
	return PermissionWrite
}

func NormalizePermission(permission string) string {
	permission = strings.ToLower(strings.TrimSpace(permission))
	switch permission {
	case "", PermissionView, PermissionWrite, PermissionAdmin:
		return permission
	default:
		return PermissionWrite
	}
}

func capabilityDomain(capability string) string {
	capability = strings.TrimSpace(capability)
	if capability == "" {
		return ""
	}
	if idx := strings.Index(capability, "."); idx > 0 {
		return capability[:idx]
	}
	return ""
}
