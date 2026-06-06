// Package policy provides policy-as-code authorization using a Cedar-inspired syntax.
// It allows defining fine-grained access control policies that can be evaluated at runtime.
//
// Usage:
//
//	engine := policy.NewEngine()
//	engine.AddPolicy(policy.Policy{
//	    ID: "admin-access",
//	    Effect: policy.Allow,
//	    Principal: policy.Principal{Type: "User", Roles: []string{"admin"}},
//	    Action: []string{"*"},
//	    Resource: policy.Resource{Type: "Document"},
//	})
//
//	decision := engine.Evaluate(ctx, policy.Request{
//	    Principal: policy.Principal{ID: "user-123", Type: "User", Roles: []string{"admin"}},
//	    Action: "read",
//	    Resource: policy.Resource{Type: "Document", ID: "doc-456"},
//	})
package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

// Effect represents the effect of a policy (allow or deny).
type Effect string

const (
	Allow Effect = "allow"
	Deny  Effect = "deny"
)

// Decision represents an authorization decision.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
)

// Principal represents an entity making a request.
type Principal struct {
	ID         string           `json:"id"`
	Type       string           `json:"type"`
	Roles      []string         `json:"roles,omitempty"`
	Groups     []string         `json:"groups,omitempty"`
	Attributes extension.Object `json:"attributes,omitempty"`
}

// Resource represents the target of an action.
type Resource struct {
	ID         string           `json:"id"`
	Type       string           `json:"type"`
	Owner      string           `json:"owner,omitempty"`
	OrgID      string           `json:"org_id,omitempty"`
	Attributes extension.Object `json:"attributes,omitempty"`
}

// Condition represents a policy condition.
type Condition struct {
	// Field is the field to evaluate (e.g., "resource.owner", "principal.attributes.department").
	Field string `json:"field"`
	// Operator is the comparison operator.
	Operator ConditionOperator `json:"operator"`
	// Value is the value to compare against.
	Value any `json:"value"`
}

// ConditionOperator represents a condition comparison operator.
type ConditionOperator string

const (
	OpEquals      ConditionOperator = "equals"
	OpNotEquals   ConditionOperator = "not_equals"
	OpContains    ConditionOperator = "contains"
	OpIn          ConditionOperator = "in"
	OpNotIn       ConditionOperator = "not_in"
	OpMatches     ConditionOperator = "matches" // regex
	OpGreaterThan ConditionOperator = "gt"
	OpLessThan    ConditionOperator = "lt"
	OpExists      ConditionOperator = "exists"
)

// Policy represents an authorization policy.
type Policy struct {
	// ID is the unique identifier for the policy.
	ID string `json:"id"`

	// Description describes the policy's purpose.
	Description string `json:"description,omitempty"`

	// Effect is whether this policy allows or denies (default: allow).
	Effect Effect `json:"effect"`

	// Priority determines evaluation order (higher = evaluated first).
	Priority int `json:"priority,omitempty"`

	// Principal defines who this policy applies to.
	Principal *PrincipalMatcher `json:"principal,omitempty"`

	// Actions are the actions this policy covers (supports wildcards).
	Actions []string `json:"actions"`

	// Resource defines what resources this policy covers.
	Resource *ResourceMatcher `json:"resource,omitempty"`

	// Conditions are additional conditions that must be met.
	Conditions []Condition `json:"conditions,omitempty"`
}

// PrincipalMatcher defines criteria for matching principals.
type PrincipalMatcher struct {
	Type   string   `json:"type,omitempty"`
	IDs    []string `json:"ids,omitempty"`
	Roles  []string `json:"roles,omitempty"`
	Groups []string `json:"groups,omitempty"`
	AnyOf  bool     `json:"any_of,omitempty"` // If true, match any; if false, match all
}

// ResourceMatcher defines criteria for matching resources.
type ResourceMatcher struct {
	Type  string   `json:"type,omitempty"`
	IDs   []string `json:"ids,omitempty"`
	Owner string   `json:"owner,omitempty"` // Special: "${principal.id}" for owner match
	OrgID string   `json:"org_id,omitempty"`
}

// Request represents an authorization request.
type Request struct {
	Principal Principal        `json:"principal"`
	Action    string           `json:"action"`
	Resource  Resource         `json:"resource"`
	Context   extension.Object `json:"context,omitempty"`
}

// Result represents the result of policy evaluation.
type Result struct {
	Decision    Decision `json:"decision"`
	PolicyID    string   `json:"policy_id,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// Engine evaluates policies.
type Engine struct {
	mu       sync.RWMutex
	policies []Policy
}

// NewEngine creates a new policy engine.
func NewEngine() *Engine {
	return &Engine{
		policies: make([]Policy, 0),
	}
}

// AddPolicy adds a policy to the engine.
func (e *Engine) AddPolicy(p Policy) {
	if p.Effect == "" {
		p.Effect = Allow
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policies = append(e.policies, p)
	// Sort by priority (higher first)
	for i := len(e.policies) - 1; i > 0; i-- {
		if e.policies[i].Priority > e.policies[i-1].Priority {
			e.policies[i], e.policies[i-1] = e.policies[i-1], e.policies[i]
		}
	}
}

// RemovePolicy removes a policy by ID.
func (e *Engine) RemovePolicy(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, p := range e.policies {
		if p.ID == id {
			e.policies = append(e.policies[:i], e.policies[i+1:]...)
			return
		}
	}
}

// LoadPolicies loads policies from JSON.
func (e *Engine) LoadPolicies(data []byte) error {
	var policies []Policy
	if err := json.Unmarshal(data, &policies); err != nil {
		return err
	}
	for _, p := range policies {
		e.AddPolicy(p)
	}
	return nil
}

// Evaluate evaluates a request against all policies.
// Default-deny: returns deny if no policy explicitly allows.
func (e *Engine) Evaluate(ctx context.Context, req Request) Result {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var matchedAllow *Policy
	diagnostics := make([]string, 0)

	for i := range e.policies {
		p := &e.policies[i]
		matched, reason := e.matchPolicy(p, req)
		diagnostics = append(diagnostics, fmt.Sprintf("policy %s: %s", p.ID, reason))

		if matched {
			if p.Effect == Deny {
				// Explicit deny always wins
				return Result{
					Decision:    DecisionDeny,
					PolicyID:    p.ID,
					Reason:      fmt.Sprintf("explicitly denied by policy %s", p.ID),
					Diagnostics: diagnostics,
				}
			}
			if matchedAllow == nil {
				matchedAllow = p
			}
		}
	}

	if matchedAllow != nil {
		return Result{
			Decision:    DecisionAllow,
			PolicyID:    matchedAllow.ID,
			Reason:      fmt.Sprintf("allowed by policy %s", matchedAllow.ID),
			Diagnostics: diagnostics,
		}
	}

	return Result{
		Decision:    DecisionDeny,
		Reason:      "no matching policy found (default deny)",
		Diagnostics: diagnostics,
	}
}

// matchPolicy checks if a policy matches the request.
func (e *Engine) matchPolicy(p *Policy, req Request) (bool, string) {
	// Check principal
	if p.Principal != nil {
		if !e.matchPrincipal(p.Principal, req.Principal) {
			return false, "principal mismatch"
		}
	}

	// Check action
	if !e.matchAction(p.Actions, req.Action) {
		return false, "action mismatch"
	}

	// Check resource
	if p.Resource != nil {
		if !e.matchResource(p.Resource, req.Resource, req.Principal) {
			return false, "resource mismatch"
		}
	}

	// Check conditions
	for _, cond := range p.Conditions {
		if !e.evaluateCondition(cond, req) {
			return false, fmt.Sprintf("condition %s %s failed", cond.Field, cond.Operator)
		}
	}

	return true, "matched"
}

// matchPrincipal checks if a principal matches the matcher.
func (e *Engine) matchPrincipal(m *PrincipalMatcher, p Principal) bool {
	// Check type
	if m.Type != "" && m.Type != "*" && m.Type != p.Type {
		return false
	}

	// Check IDs
	if len(m.IDs) > 0 {
		found := false
		for _, id := range m.IDs {
			if id == p.ID || id == "*" {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check roles
	if len(m.Roles) > 0 {
		if m.AnyOf {
			// Match any role
			found := false
			for _, mr := range m.Roles {
				for _, pr := range p.Roles {
					if mr == pr || mr == "*" {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				return false
			}
		} else {
			// Match all roles
			for _, mr := range m.Roles {
				found := false
				for _, pr := range p.Roles {
					if mr == pr || mr == "*" {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
		}
	}

	// Check groups
	if len(m.Groups) > 0 {
		found := false
		for _, mg := range m.Groups {
			for _, pg := range p.Groups {
				if mg == pg || mg == "*" {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// matchAction checks if an action matches the allowed actions.
func (e *Engine) matchAction(allowed []string, action string) bool {
	for _, a := range allowed {
		if a == "*" || a == action {
			return true
		}
		// Check wildcard patterns like "document:*"
		if strings.HasSuffix(a, ":*") {
			prefix := strings.TrimSuffix(a, "*")
			if strings.HasPrefix(action, prefix) {
				return true
			}
		}
	}
	return false
}

// matchResource checks if a resource matches the matcher.
func (e *Engine) matchResource(m *ResourceMatcher, r Resource, p Principal) bool {
	// Check type
	if m.Type != "" && m.Type != "*" && m.Type != r.Type {
		return false
	}

	// Check IDs
	if len(m.IDs) > 0 {
		found := false
		for _, id := range m.IDs {
			if id == r.ID || id == "*" {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check owner
	if m.Owner != "" {
		owner := m.Owner
		// Replace ${principal.id} with actual principal ID
		if owner == "${principal.id}" {
			owner = p.ID
		}
		if owner != r.Owner {
			return false
		}
	}

	// Check org ID
	if m.OrgID != "" && m.OrgID != r.OrgID {
		return false
	}

	return true
}

// evaluateCondition evaluates a single condition.
func (e *Engine) evaluateCondition(cond Condition, req Request) bool {
	value := e.resolveField(cond.Field, req)

	switch cond.Operator {
	case OpEquals:
		return fmt.Sprintf("%v", value) == fmt.Sprintf("%v", cond.Value)

	case OpNotEquals:
		return fmt.Sprintf("%v", value) != fmt.Sprintf("%v", cond.Value)

	case OpContains:
		if arr, ok := value.([]string); ok {
			if slices.Contains(arr, fmt.Sprintf("%v", cond.Value)) {
				return true
			}
		}
		if arr, ok := value.([]any); ok {
			needle := fmt.Sprintf("%v", cond.Value)
			for _, item := range arr {
				if fmt.Sprintf("%v", item) == needle {
					return true
				}
			}
		}
		if str, ok := value.(string); ok {
			return strings.Contains(str, fmt.Sprintf("%v", cond.Value))
		}
		return false

	case OpIn:
		if arr, ok := cond.Value.([]any); ok {
			strVal := fmt.Sprintf("%v", value)
			for _, v := range arr {
				if fmt.Sprintf("%v", v) == strVal {
					return true
				}
			}
		}
		return false

	case OpNotIn:
		if arr, ok := cond.Value.([]any); ok {
			strVal := fmt.Sprintf("%v", value)
			for _, v := range arr {
				if fmt.Sprintf("%v", v) == strVal {
					return false
				}
			}
		}
		return true

	case OpMatches:
		if pattern, ok := cond.Value.(string); ok {
			if re, err := regexp.Compile(pattern); err == nil {
				return re.MatchString(fmt.Sprintf("%v", value))
			}
		}
		return false

	case OpExists:
		return value != nil

	default:
		return false
	}
}

// resolveField resolves a field path to a value.
func (e *Engine) resolveField(field string, req Request) any {
	parts := strings.Split(field, ".")
	if len(parts) == 0 {
		return nil
	}
	switch parts[0] {
	case "principal":
		return resolvePrincipalField(req.Principal, parts[1:])
	case "resource":
		return resolveResourceField(req.Resource, parts[1:])
	case "action":
		return req.Action
	case "context":
		return resolveObjectField(req.Context, parts[1:])
	default:
		return nil
	}
}

func resolvePrincipalField(principal Principal, parts []string) any {
	if len(parts) == 0 {
		return principal
	}
	switch parts[0] {
	case "id":
		return principal.ID
	case "type":
		return principal.Type
	case "roles":
		return principal.Roles
	case "groups":
		return principal.Groups
	case "attributes":
		return resolveObjectField(principal.Attributes, parts[1:])
	default:
		return nil
	}
}

func resolveResourceField(resource Resource, parts []string) any {
	if len(parts) == 0 {
		return resource
	}
	switch parts[0] {
	case "id":
		return resource.ID
	case "type":
		return resource.Type
	case "owner":
		return resource.Owner
	case "org_id":
		return resource.OrgID
	case "attributes":
		return resolveObjectField(resource.Attributes, parts[1:])
	default:
		return nil
	}
}

func resolveObjectField(object extension.Object, parts []string) any {
	if len(parts) == 0 {
		return object
	}
	value, ok := object[parts[0]]
	if !ok {
		return nil
	}
	if len(parts) == 1 {
		return value.Interface()
	}
	next, ok := value.ObjectValue()
	if !ok {
		return nil
	}
	return resolveObjectField(next, parts[1:])
}

// Middleware returns an HTTP middleware that enforces policies.
func Middleware(engine *Engine, resourceResolver func(*http.Request) Resource) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := PrincipalFromContext(r.Context())
			if principal == nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			resource := resourceResolver(r)
			action := strings.ToLower(r.Method) + ":" + r.URL.Path

			result := engine.Evaluate(r.Context(), Request{
				Principal: *principal,
				Action:    action,
				Resource:  resource,
			})

			if result.Decision == DecisionDeny {
				http.Error(w, "Forbidden: "+result.Reason, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type principalContextKey struct{}

// ContextWithPrincipal adds a principal to the context.
func ContextWithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, &p)
}

// PrincipalFromContext retrieves the principal from context.
func PrincipalFromContext(ctx context.Context) *Principal {
	if v := ctx.Value(principalContextKey{}); v != nil {
		return v.(*Principal)
	}
	return nil
}
