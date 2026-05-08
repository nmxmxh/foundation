package policy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
			{Field: "context.region", Operator: OpIn, Value: []interface{}{"ng", "us"}},
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
		Principal: Principal{ID: "u1", Type: "User", Roles: []string{"editor"}, Attributes: map[string]interface{}{"department": "media"}},
		Action:    "document:read",
		Resource:  Resource{ID: "doc1", Type: "Document", Owner: "u1", OrgID: "org_1", Attributes: map[string]interface{}{"classification": "internal"}},
		Context:   map[string]interface{}{"region": "ng"},
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
			Attributes: map[string]interface{}{
				"tags":   []string{"trusted", "beta"},
				"email":  "person@example.com",
				"active": true,
			},
		},
		Action:   "asset:update",
		Resource: Resource{ID: "asset_1", Type: "Asset", Owner: "u1", OrgID: "org_1"},
		Context:  map[string]interface{}{"tier": "gold"},
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
			{Field: "context.tier", Operator: OpNotIn, Value: []interface{}{"free"}},
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
