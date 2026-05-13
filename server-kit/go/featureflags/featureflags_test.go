package featureflags

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestFlagEvaluationRulesAndSources(t *testing.T) {
	m := New(Config{DefaultEnvironment: "prod"})
	defer m.Stop()
	now := time.Now()
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)
	m.SetFlag(Flag{Name: "off", Enabled: false})
	m.SetFlag(Flag{Name: "window", Enabled: true, StartTime: &future})
	m.SetFlag(Flag{Name: "ended", Enabled: true, EndTime: &past})
	m.SetFlag(Flag{Name: "env", Enabled: true, Environments: []string{"staging"}})
	m.SetFlag(Flag{Name: "users", Enabled: true, AllowedUsers: []string{"u1"}, DeniedUsers: []string{"u2"}})
	m.SetFlag(Flag{Name: "orgs", Enabled: true, AllowedOrgs: []string{"org1"}})
	m.SetFlag(Flag{Name: "rollout", Enabled: true, RolloutPercentage: 50})

	if m.IsEnabled(context.Background(), "missing") || m.IsEnabled(context.Background(), "off") {
		t.Fatal("missing/off flags should be disabled")
	}
	if m.IsEnabled(context.Background(), "window") || m.IsEnabled(context.Background(), "ended") {
		t.Fatal("time-windowed flags should be disabled")
	}
	if m.IsEnabled(context.Background(), "env") || !m.IsEnabled(context.Background(), "env", WithEnvironment("staging")) {
		t.Fatal("environment evaluation failed")
	}
	if !m.IsEnabled(context.Background(), "users", WithUser("u1")) || m.IsEnabled(context.Background(), "users", WithUser("u2")) {
		t.Fatal("user allow/deny evaluation failed")
	}
	if !m.IsEnabled(context.Background(), "orgs", WithOrg("org1")) {
		t.Fatal("org allow evaluation failed")
	}
	if m.IsEnabled(context.Background(), "rollout") {
		t.Fatal("percentage rollout requires user id")
	}
	if hashToBucket("flag", "user") != hashToBucket("flag", "user") {
		t.Fatal("hash bucket should be deterministic")
	}
	flags := m.AllFlags()
	flags["off"] = Flag{Name: "mutated", Enabled: true}
	if flag, ok := m.GetFlag("off"); !ok || flag.Name != "off" {
		t.Fatalf("GetFlag() = %+v %v", flag, ok)
	}
}

func TestManagerStopIsIdempotent(t *testing.T) {
	m := New(Config{})
	m.Stop()
	m.Stop()
}

func TestMemoryEnvJSONSourcesAndMiddleware(t *testing.T) {
	source := NewMemorySource()
	source.Set(Flag{Name: "beta", Enabled: true})
	m := New(Config{Source: source, RefreshInterval: time.Hour})
	defer m.Stop()
	if !m.IsEnabled(context.Background(), "beta") {
		t.Fatal("expected initial memory source load")
	}
	source.Set(Flag{Name: "gamma", Enabled: true})
	deadline := time.Now().Add(500 * time.Millisecond)
	for !m.IsEnabled(context.Background(), "gamma") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !m.IsEnabled(context.Background(), "gamma") {
		t.Fatal("expected watched memory source update")
	}
	source.Delete("gamma")

	t.Setenv("FF_NEW_FLOW", "true")
	t.Setenv("FF_HALF_FLOW", "50")
	t.Setenv("FF_OFF_FLOW", "false")
	envFlags, err := NewEnvSource().Load(context.Background())
	if err != nil || !envFlags["new-flow"].Enabled || envFlags["half-flow"].RolloutPercentage != 50 || envFlags["off-flow"].Enabled {
		t.Fatalf("env flags = %+v err=%v", envFlags, err)
	}
	t.Setenv("CUSTOM_FLAG", "true")
	customFlags, _ := NewEnvSourceWithPrefix("CUSTOM_").Load(context.Background())
	if !customFlags["flag"].Enabled {
		t.Fatal("custom prefix env source failed")
	}
	if NewEnvSource().Watch(context.Background()) != nil {
		t.Fatal("env watch should be nil")
	}
	if _, err := NewJSONSource(`bad`).Load(context.Background()); err == nil {
		t.Fatal("expected bad json to fail")
	}
	jsonFlags, err := NewJSONSource(`[{"name":"json","enabled":true}]`).Load(context.Background())
	if err != nil || !jsonFlags["json"].Enabled {
		t.Fatalf("json flags = %+v err=%v", jsonFlags, err)
	}
	tmp, err := os.CreateTemp(t.TempDir(), "flags-*.json")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	_, _ = tmp.WriteString(`[{"name":"file","enabled":true}]`)
	_ = tmp.Close()
	fileFlags, err := NewJSONFileSource(tmp.Name()).Load(context.Background())
	if err != nil || !fileFlags["file"].Enabled {
		t.Fatalf("file flags = %+v err=%v", fileFlags, err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(ContextWithManager(ContextWithEvaluation(req.Context(), &EvaluationContext{UserID: "u1", OrgID: "org1"}), m))
	if ManagerFromContext(req.Context()) == nil || EvaluationFromContext(req.Context()).UserID != "u1" {
		t.Fatal("context helpers failed")
	}
	if IsEnabledFromContext(context.Background(), "beta") {
		t.Fatal("missing manager should disable flag")
	}
	rec := httptest.NewRecorder()
	UserMiddleware(func(*http.Request) string { return "u1" }, func(*http.Request) string { return "org1" })(
		Middleware(m)(RequireFlag(m, "beta")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !IsEnabledFromContext(r.Context(), "beta") {
				t.Fatal("flag should be enabled from context")
			}
			w.WriteHeader(http.StatusAccepted)
		}))),
	).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("middleware status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	RequireFlag(m, "missing")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing flag status = %d", rec.Code)
	}
}

func TestManagerWatchChangesAndAttributes(t *testing.T) {
	source := NewMemorySource()
	changes := make(chan map[string]Flag, 1)
	m := New(Config{
		Source:          source,
		RefreshInterval: time.Hour,
		OnChange: func(oldFlags, newFlags map[string]Flag) {
			if _, existed := oldFlags["watched"]; existed {
				t.Errorf("old flags should not already contain watched flag")
			}
			changes <- newFlags
		},
	})
	defer m.Stop()

	source.Set(Flag{Name: "watched", Enabled: true})
	select {
	case got := <-changes:
		if !got["watched"].Enabled {
			t.Fatalf("watch update = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for watched flag update")
	}

	eval := &EvaluationContext{}
	WithAttribute("plan", "pro")(eval)
	if eval.Attributes["plan"] != "pro" {
		t.Fatalf("attribute option did not initialize map: %+v", eval.Attributes)
	}
	if NewJSONSource(`[]`).Watch(context.Background()) != nil {
		t.Fatal("json watch should be nil")
	}
}
