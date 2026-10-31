package security

import (
	"context"
	"strings"
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
)

// CapabilityMask is a compact, zero-allocation 64-bit bitmask representing
// authorized capabilities. Bitwise authorization evaluates in O(1) CPU time:
// Authorized = (ActorMask & RequiredMask) == RequiredMask.
type CapabilityMask uint64

const (
	// CapabilityMaskAll grants all capabilities (equivalent to wildcard "*").
	CapabilityMaskAll CapabilityMask = ^CapabilityMask(0)
	// CapabilityMaskNone grants no capabilities.
	CapabilityMaskNone CapabilityMask = 0
	// MaxIndexedCapabilities is the maximum number of distinct capabilities
	// that can be indexed into a single 64-bit mask.
	MaxIndexedCapabilities = 64
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

// Authorizer holds role-capability mappings with O(1) bitmask fast-paths.
type Authorizer struct {
	byRole    map[string]map[string]struct{}
	roleMasks map[string]CapabilityMask
	capBits   map[string]CapabilityMask
	mu        sync.RWMutex // private: an embedded mutex would export Lock and Unlock
}

func NewAuthorizer(templates []RoleTemplate) *Authorizer {
	if len(templates) == 0 {
		templates = DefaultRoleTemplates()
	}
	byRole := map[string]map[string]struct{}{}
	roleMasks := map[string]CapabilityMask{}
	capBits := map[string]CapabilityMask{}
	nextBit := CapabilityMask(1)

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
			if capability == "*" {
				roleMasks[role] = CapabilityMaskAll
			} else if !strings.Contains(capability, "*") {
				bit, exists := capBits[capability]
				if !exists && nextBit != 0 {
					bit = nextBit
					capBits[capability] = bit
					nextBit <<= 1
				}
				if bit != 0 {
					roleMasks[role] |= bit
				}
			}
		}
	}

	// Resolve wildcard prefixes like "domain.*" across known capability bits
	for _, tpl := range templates {
		role := strings.TrimSpace(tpl.Role)
		if role == "" {
			// Same rule as the first pass: an empty role never gets a mask, so anonymous requests get nothing.
			continue
		}
		for _, capability := range tpl.Capabilities {
			capability = strings.TrimSpace(capability)
			if prefix, ok := strings.CutSuffix(capability, ".*"); ok {
				for capName, bit := range capBits {
					if strings.HasPrefix(capName, prefix+".") {
						roleMasks[role] |= bit
					}
				}
			}
		}
	}

	return &Authorizer{
		byRole:    byRole,
		roleMasks: roleMasks,
		capBits:   capBits,
	}
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

// CapabilityBit returns the pre-indexed 64-bit mask for capability.
func (a *Authorizer) CapabilityBit(capability string) (CapabilityMask, bool) {
	if a == nil {
		return CapabilityMaskNone, false
	}
	a.mu.RLock()
	bit, ok := a.capBits[strings.TrimSpace(capability)]
	a.mu.RUnlock()
	return bit, ok
}

// RoleMask returns the composite CapabilityMask granted to role.
func (a *Authorizer) RoleMask(role string) CapabilityMask {
	if a == nil {
		return CapabilityMaskNone
	}
	a.mu.RLock()
	mask := a.roleMasks[strings.TrimSpace(role)]
	a.mu.RUnlock()
	return mask
}

// CapabilityMaskFor returns the combined mask for one or more capabilities.
// If any capability is not indexed, it returns CapabilityMaskNone and false.
func (a *Authorizer) CapabilityMaskFor(capabilities ...string) (CapabilityMask, bool) {
	if a == nil || len(capabilities) == 0 {
		return CapabilityMaskNone, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	var combined CapabilityMask
	for _, capName := range capabilities {
		bit, ok := a.capBits[strings.TrimSpace(capName)]
		if !ok {
			return CapabilityMaskNone, false
		}
		combined |= bit
	}
	return combined, true
}

// CheckMask verifies that actorMask satisfies requiredMask in O(1) bitwise time.
func CheckMask(actorMask, requiredMask CapabilityMask) bool {
	return (actorMask & requiredMask) == requiredMask
}

// RegisterCapability dynamically adds capability to the bitmask index.
func (a *Authorizer) RegisterCapability(capability string) (CapabilityMask, bool) {
	if a == nil {
		return CapabilityMaskNone, false
	}
	capName := strings.TrimSpace(capability)
	if capName == "" {
		return CapabilityMaskNone, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if bit, ok := a.capBits[capName]; ok {
		return bit, true
	}
	if len(a.capBits) >= MaxIndexedCapabilities {
		return CapabilityMaskNone, false
	}
	bit := CapabilityMask(1) << len(a.capBits)
	a.capBits[capName] = bit
	// Walk byRole, not roleMasks. A role whose wildcards matched nothing at build time has no mask entry yet.
	for role, roleCaps := range a.byRole {
		if a.roleMasks[role] == CapabilityMaskAll {
			continue
		}
		for granted := range roleCaps {
			if capabilityGranted(granted, capName) {
				a.roleMasks[role] |= bit
				break
			}
		}
	}
	return bit, true
}

func (a *Authorizer) Can(role, capability string, directCapabilities []string) bool {
	if a != nil && len(directCapabilities) == 0 && role != "" {
		a.mu.RLock()
		bit, okCap := a.capBits[capability]
		mask, okRole := a.roleMasks[role]
		a.mu.RUnlock()
		if okCap && okRole && (mask&bit) == bit {
			return true
		}
	}
	return a.CanAccess(role, capability, "", directCapabilities)
}

func (a *Authorizer) CanAccess(role, capability, permission string, directCapabilities []string) bool {
	role = strings.TrimSpace(role)
	capability = strings.TrimSpace(capability)
	permission = NormalizePermission(permission)
	if role == "" || capability == "" {
		return false
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
