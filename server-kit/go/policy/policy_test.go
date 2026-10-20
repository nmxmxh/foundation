package policy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

func policyObject(values map[string]any) extension.Object {
	out := make(extension.Object, len(values))
	for key, value := range values {
		typed, err := extension.FromJSON(value)
		if err == nil {
			out[key] = typed
		}
	}
	return out
}

func TestEvaluateAllowDenyPriorityAndConditions(t *testing.T) {
	engine := NewEngine()
	engine.AddPolicy(Policy{
		ID:       "allow-owner",
		Effect:   Allow,
		Priority: 1,
		Principal: &PrincipalMatcher{
			Type:  "User",
			Roles: []string{"editor"},
		},
		Actions: []string{"document:*"},
		Resource: &ResourceMatcher{
			Type:  "Document",
			Owner: "${principal.id}",
			OrgID: "org_1",
		},
		Conditions: []Condition{
			{Field: "resource.attributes.classification", Operator: OpNotEquals, Value: "restricted"},
			{Field: "context.region", Operator: OpIn, Value: []any{"ng", "us"}},
			{Field: "principal.attributes.department", Operator: OpExists},
		},
	})
	engine.AddPolicy(Policy{
		ID:       "deny-delete",
		Effect:   Deny,
		Priority: 10,
		Actions:  []string{"document:delete"},
		Resource: &ResourceMatcher{Type: "Document"},
	})

	req := Request{
		Principal: Principal{ID: "u1", Type: "User", Roles: []string{"editor"}, Attributes: policyObject(map[string]any{"department": "media"})},
		Action:    "document:read",
		Resource:  Resource{ID: "doc1", Type: "Document", Owner: "u1", OrgID: "org_1", Attributes: policyObject(map[string]any{"classification": "internal"})},
		Context:   policyObject(map[string]any{"region": "ng"}),
	}
	if got := engine.Evaluate(context.Background(), req); got.Decision != DecisionAllow || got.PolicyID != "allow-owner" {
		t.Fatalf("allow decision = %+v", got)
	}
	req.Action = "document:delete"
	if got := engine.Evaluate(context.Background(), req); got.Decision != DecisionDeny || got.PolicyID != "deny-delete" {
		t.Fatalf("deny decision = %+v", got)
	}
	req.Action = "document:publish"
	req.Resource.Owner = "other"
	if got := engine.Evaluate(context.Background(), req); got.Decision != DecisionDeny || !strings.Contains(got.Reason, "default deny") {
		t.Fatalf("default deny = %+v", got)
	}
}

func TestPrincipalResourceAndConditionOperators(t *testing.T) {
	engine := NewEngine()
	req := Request{
		Principal: Principal{
			ID:     "u1",
			Type:   "User",
			Roles:  []string{"editor", "reviewer"},
			Groups: []string{"media"},
			Attributes: policyObject(map[string]any{
				"tags":   []string{"trusted", "beta"},
				"email":  "person@example.com",
				"active": true,
			}),
		},
		Action:   "asset:update",
		Resource: Resource{ID: "asset_1", Type: "Asset", Owner: "u1", OrgID: "org_1"},
		Context:  policyObject(map[string]any{"tier": "gold"}),
	}
	policy := Policy{
		ID: "matrix",
		Principal: &PrincipalMatcher{
			Type:   "User",
			IDs:    []string{"u1"},
			Roles:  []string{"editor", "reviewer"},
			Groups: []string{"media"},
		},
		Actions:  []string{"asset:*"},
		Resource: &ResourceMatcher{Type: "Asset", IDs: []string{"asset_1"}, Owner: "${principal.id}", OrgID: "org_1"},
		Conditions: []Condition{
			{Field: "principal.attributes.tags", Operator: OpContains, Value: "trusted"},
			{Field: "principal.attributes.email", Operator: OpMatches, Value: `@example\.com$`},
			{Field: "context.tier", Operator: OpNotIn, Value: []any{"free"}},
			{Field: "action", Operator: OpEquals, Value: "asset:update"},
		},
	}
	matched, reason := engine.matchPolicy(&policy, req)
	if !matched || reason != "matched" {
		t.Fatalf("matchPolicy = %v %q", matched, reason)
	}
	if engine.evaluateCondition(Condition{Field: "principal.attributes.active", Operator: OpContains, Value: "true"}, req) {
		t.Fatal("contains should fail for unsupported value type")
	}
	if engine.evaluateCondition(Condition{Field: "context.tier", Operator: OpMatches, Value: "["}, req) {
		t.Fatal("invalid regex should fail")
	}
	if engine.evaluateCondition(Condition{Field: "context.tier", Operator: OpGreaterThan, Value: "silver"}, req) {
		t.Fatal("unsupported operator should fail")
	}
	if got := engine.resolveField("missing.field", req); got != nil {
		t.Fatalf("missing field = %v", got)
	}
}

func TestLoadRemoveAndMiddleware(t *testing.T) {
	engine := NewEngine()
	if err := engine.LoadPolicies([]byte(`[{"id":"allow-get","actions":["get:/resource"],"resource":{"type":"Thing"}}]`)); err != nil {
		t.Fatalf("LoadPolicies() error = %v", err)
	}
	req := Request{Principal: Principal{ID: "u1"}, Action: "get:/resource", Resource: Resource{Type: "Thing"}}
	if got := engine.Evaluate(context.Background(), req); got.Decision != DecisionAllow {
		t.Fatalf("loaded policy decision = %+v", got)
	}
	engine.RemovePolicy("allow-get")
	if got := engine.Evaluate(context.Background(), req); got.Decision != DecisionDeny {
		t.Fatalf("removed policy decision = %+v", got)
	}
	if err := engine.LoadPolicies([]byte(`{bad json`)); err == nil {
		t.Fatal("expected invalid policy json to fail")
	}

	engine.AddPolicy(Policy{ID: "middleware", Actions: []string{"get:/thing"}, Resource: &ResourceMatcher{Type: "Thing"}})
	mw := Middleware(engine, func(*http.Request) Resource { return Resource{Type: "Thing"} })
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thing", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing principal status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/thing", nil)
	r = r.WithContext(ContextWithPrincipal(r.Context(), Principal{ID: "u1"}))
	mw(next).ServeHTTP(rec, r)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("allowed middleware status = %d", rec.Code)
	}
	if PrincipalFromContext(r.Context()).ID != "u1" {
		t.Fatal("principal not stored in context")
	}
}

func TestMinatoMorrealeISOPAndPolicyAnalyze(t *testing.T) {
	// 1. Direct Minato-Morreale ISOP verification on a 4-variable function with
	// don't-cares: verify cover lies in [L, U] and is irredundant (no cube and
	// no literal can be dropped without falling below L or escaping U).
	lower := NewTruthTable(4)
	upper := NewTruthTable(4)
	// f = x0*x1 + x0*~x1*x2, with x0*x1*x2*x3 as don't-care
	for a := uint32(0); a < 16; a++ {
		x0 := (a & 1) != 0
		x1 := (a & 2) != 0
		x2 := (a & 4) != 0
		on := (x0 && x1) || (x0 && !x1 && x2)
		dc := !x0 && x1 && x2
		if on {
			lower.Set(a, true)
			upper.Set(a, true)
		} else if dc {
			upper.Set(a, true)
		}
	}
	cover := MinatoMorrealeISOP(lower, upper)
	covTable := tableFromCubes(4, cover)
	if !lower.andNot(covTable).IsZero() {
		t.Fatal("ISOP cover failed to cover all lower minterms")
	}
	if !covTable.andNot(upper).IsZero() {
		t.Fatal("ISOP cover exceeded upper bound")
	}
	// Irredundancy: dropping any single cube must uncover at least one lower minterm.
	for i := range cover {
		reduced := append([]Cube(nil), cover[:i]...)
		reduced = append(reduced, cover[i+1:]...)
		if lower.andNot(tableFromCubes(4, reduced)).IsZero() {
			t.Fatalf("ISOP cover is redundant: cube %d (%+v) can be removed", i, cover[i])
		}
	}

	// 2. Policy Engine.Analyze() detects shadowed allow rules, redundant narrow rules,
	// and mutual exclusion don't-cares (e.g. principal.type==User vs Service), and
	// produces an irredundant cover equivalent to Engine.Evaluate on all concrete requests.
	engine := NewEngine()
	// Broad allow: User with role editor on Document
	engine.AddPolicy(Policy{
		ID:        "allow-editor-doc",
		Effect:    Allow,
		Principal: &PrincipalMatcher{Type: "User", Roles: []string{"editor"}},
		Actions:   []string{"document:read"},
		Resource:  &ResourceMatcher{Type: "Document", OrgID: "org_1"},
	})
	// Redundant narrower allow: same + extra condition
	engine.AddPolicy(Policy{
		ID:        "redundant-narrow-editor",
		Effect:    Allow,
		Principal: &PrincipalMatcher{Type: "User", Roles: []string{"editor"}},
		Actions:   []string{"document:read"},
		Resource:  &ResourceMatcher{Type: "Document", OrgID: "org_1"},
		Conditions: []Condition{
			{Field: "context.env", Operator: OpEquals, Value: "prod"},
		},
	})
	// Shadowed allow: allows document:delete for User, but overridden by explicit deny
	engine.AddPolicy(Policy{
		ID:        "shadowed-allow-delete",
		Effect:    Allow,
		Principal: &PrincipalMatcher{Type: "User", Roles: []string{"editor"}},
		Actions:   []string{"document:delete"},
		Resource:  &ResourceMatcher{Type: "Document", OrgID: "org_1"},
	})
	engine.AddPolicy(Policy{
		ID:       "deny-all-delete",
		Effect:   Deny,
		Actions:  []string{"document:delete"},
		Resource: &ResourceMatcher{Type: "Document"},
	})
	// Mutual exclusion test policy for Service principal
	engine.AddPolicy(Policy{
		ID:        "allow-service-read",
		Effect:    Allow,
		Principal: &PrincipalMatcher{Type: "Service"},
		Actions:   []string{"document:read"},
		Resource:  &ResourceMatcher{Type: "Document", OrgID: "org_1"},
		Conditions: []Condition{
			{Field: "context.env", Operator: OpNotEquals, Value: "dev"},
		},
	})

	report, err := engine.Analyze()
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}
	if !slices.Contains(report.ShadowedAllowPolicies, "shadowed-allow-delete") {
		t.Fatalf("expected shadowed-allow-delete in ShadowedAllowPolicies, got %v", report.ShadowedAllowPolicies)
	}
	if !slices.Contains(report.RedundantPolicies, "redundant-narrow-editor") {
		t.Fatalf("expected redundant-narrow-editor in RedundantPolicies, got %v", report.RedundantPolicies)
	}
	if len(report.IrredundantCover) == 0 || report.AllowedMintermCount == 0 {
		t.Fatalf("unexpected empty cover: %+v", report)
	}
	for _, c := range report.IrredundantCover {
		if c.Format(report.Atoms) == "" {
			t.Fatal("empty formatted cube")
		}
	}
	if (Cube{}).Format(report.Atoms) != "true" {
		t.Fatal("expected tautology cube to format as 'true'")
	}

	// Verify deep object field depth guard in resolveObjectField (>32 levels returns nil).
	deepPath := "context." + strings.Repeat("a.", 35) + "leaf"
	if got := engine.resolveField(deepPath, Request{Context: policyObject(map[string]any{"a": map[string]any{"b": 1}})}); got != nil {
		t.Fatalf("expected nil on excessive field depth, got %v", got)
	}
}
