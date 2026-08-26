package profiling

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHandlerRequiresEnablementAndAuthorization(t *testing.T) {
	disabled := httptest.NewRecorder()
	Handler(Config{}).ServeHTTP(disabled, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if disabled.Code != http.StatusNotFound {
		t.Fatalf("disabled status = %d, want 404", disabled.Code)
	}

	forbidden := httptest.NewRecorder()
	Handler(Config{Enabled: true, Authorize: func(*http.Request) bool { return false }}).
		ServeHTTP(forbidden, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("forbidden status = %d, want 403", forbidden.Code)
	}
}

func TestFromEnvGatesEnabledAndPrefix(t *testing.T) {
	off := FromEnv(func(string) string { return "" })
	if off.Enabled || off.AdminPathPrefix != "" {
		t.Fatalf("empty env config = %+v, want disabled default prefix", off)
	}

	getenv := func(key string) string {
		switch key {
		case EnvEnabled:
			return "true"
		case EnvPrefix:
			return " /admin/profile/ "
		default:
			return ""
		}
	}
	on := FromEnv(getenv)
	if !on.Enabled || on.AdminPathPrefix != "/admin/profile/" {
		t.Fatalf("env config = %+v, want enabled whitespace-trimmed prefix", on)
	}
	served := httptest.NewRecorder()
	Handler(on).ServeHTTP(served, httptest.NewRequest(http.MethodGet, "/admin/profile/", nil))
	if served.Code != http.StatusOK {
		t.Fatalf("custom prefix status = %d, want 200", served.Code)
	}

	for _, value := range []string{"0", "false", "no", "", "  ", "off"} {
		if FromEnv(func(string) string { return value }).Enabled {
			t.Fatalf("value %q enabled profiling", value)
		}
	}
}

func TestHandlerServesDefaultAndCustomIndex(t *testing.T) {
	allowed := httptest.NewRecorder()
	Handler(Config{Enabled: true}).ServeHTTP(allowed, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if allowed.Code != http.StatusOK {
		t.Fatalf("default index status = %d, want 200", allowed.Code)
	}

	custom := httptest.NewRecorder()
	Handler(Config{
		Enabled:         true,
		AdminPathPrefix: "/admin/profile/",
		Authorize:       func(*http.Request) bool { return true },
	}).ServeHTTP(custom, httptest.NewRequest(http.MethodGet, "/admin/profile/", nil))
	if custom.Code != http.StatusOK {
		t.Fatalf("custom index status = %d, want 200", custom.Code)
	}
}

// A configured prefix must serve the runtime's named profiles, not the index
// page. pprof.Index only dispatches names under the literal "/debug/pprof/",
// so before namedProfileHandler a request for a heap profile at a custom
// prefix answered 200 with index HTML — a success carrying the wrong body,
// which reads downstream as a corrupt profile rather than a routing bug.
func TestNamedProfilesRouteUnderCustomPrefix(t *testing.T) {
	const prefix = "/admin/profile"
	handler := Handler(Config{Enabled: true, AdminPathPrefix: prefix})

	for _, name := range []string{"heap", "goroutine", "allocs", "mutex", "block"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, prefix+"/"+name+"?debug=1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", name, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "Types of profiles available") {
			t.Fatalf("%s served the index page instead of a profile", name)
		}
	}

	// The bare prefix still renders the index, and an unknown name still 404s
	// through pprof's own handler rather than silently falling back to it.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, prefix+"/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Types of profiles available") {
		t.Fatalf("index status = %d body-is-index = %v", rec.Code, strings.Contains(rec.Body.String(), "Types of profiles available"))
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, prefix+"/not-a-profile", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown profile status = %d, want 404", rec.Code)
	}

	// The default mount keeps the same behaviour it always had.
	rec = httptest.NewRecorder()
	Handler(Config{Enabled: true}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, DefaultPathPrefix+"/heap?debug=1", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Types of profiles available") {
		t.Fatalf("default prefix heap status = %d, served index = %v", rec.Code, strings.Contains(rec.Body.String(), "Types of profiles available"))
	}
}

// The mutex and block profiles report nothing until their sampling rates are
// armed, so an unarmed process answers a contention question with a valid,
// empty profile. These are the rates that make the answer real.
func TestRatesFromEnvFallsBackToSampledDefaults(t *testing.T) {
	// A disabled environment must not carry rates at all: nothing should be
	// armable from a config that never asked for profiling.
	if disabled := FromEnv(func(string) string { return "" }); disabled.Rates != (RuntimeRates{}) {
		t.Fatalf("disabled config carried rates %+v", disabled.Rates)
	}
	if enabled := FromEnv(func(key string) string {
		if key == EnvEnabled {
			return "true"
		}
		return ""
	}); enabled.Rates != SampledRates() {
		t.Fatalf("enabled config rates = %+v, want %+v", enabled.Rates, SampledRates())
	}

	defaults := ratesFromEnv(func(string) string { return "" })
	if defaults != SampledRates() {
		t.Fatalf("empty env rates = %+v, want %+v", defaults, SampledRates())
	}

	overridden := ratesFromEnv(func(key string) string {
		switch key {
		case EnvMutexFraction:
			return "7"
		case EnvBlockRate:
			return " 250 "
		default:
			return ""
		}
	})
	if overridden.MutexProfileFraction != 7 || overridden.BlockProfileRate != 250 {
		t.Fatalf("overridden rates = %+v", overridden)
	}

	// Negative asks for that profile to stay off; unparseable falls back
	// rather than silently disabling a profile the operator asked for.
	off := ratesFromEnv(func(key string) string {
		if key == EnvMutexFraction {
			return "-1"
		}
		return "not-a-number"
	})
	if off.MutexProfileFraction != 0 || off.BlockProfileRate != SampledRates().BlockProfileRate {
		t.Fatalf("negative/unparseable rates = %+v", off)
	}
}

// Arming the rates must make the mutex profile actually record contention.
func TestApplyRatesArmsTheMutexProfile(t *testing.T) {
	previous := runtime.SetMutexProfileFraction(-1)
	t.Cleanup(func() { runtime.SetMutexProfileFraction(previous) })

	applyRates(RuntimeRates{MutexProfileFraction: 1, BlockProfileRate: 0})
	if got := runtime.SetMutexProfileFraction(-1); got != 1 {
		t.Fatalf("mutex profile fraction = %d, want 1", got)
	}

	// A zero rate leaves the current setting alone rather than switching a
	// profile off underneath an operator who armed it deliberately.
	applyRates(RuntimeRates{})
	if got := runtime.SetMutexProfileFraction(-1); got != 1 {
		t.Fatalf("zero rate changed the fraction to %d", got)
	}
}

// The block profile needs arming just as the mutex one does — unarmed, it is
// the same silent empty answer. The runtime exposes no getter for the block
// rate, so a recorded blocking handoff is the only observable proof it took.
func TestApplyRatesArmsBlockProfiling(t *testing.T) {
	mutexBefore := runtime.SetMutexProfileFraction(-1)
	t.Cleanup(func() {
		runtime.SetMutexProfileFraction(mutexBefore)
		runtime.SetBlockProfileRate(0)
	})

	before := blockProfileEvents(t)
	applyRates(RuntimeRates{MutexProfileFraction: 3, BlockProfileRate: 1})
	if fraction := runtime.SetMutexProfileFraction(-1); fraction != 3 {
		t.Fatalf("mutex profile fraction = %d, want 3", fraction)
	}

	handoff := make(chan int)
	released := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Millisecond)
		<-handoff
		close(released)
	}()
	handoff <- 1
	<-released

	if after := blockProfileEvents(t); after <= before {
		t.Fatalf("block profile events %d -> %d; the rate never took", before, after)
	}
}

// blockProfileEvents totals the recorded blocking events.
//
// Profile.Count reports distinct stacks, so a second run of this test through
// the same call site leaves it at one and a delta on it can never grow. The
// debug=1 rendering carries "<cycles> <count> @ <stack>" per record, and the
// count column does grow, which is what makes the assertion survive -count>1.
func blockProfileEvents(t *testing.T) int {
	t.Helper()
	var rendered bytes.Buffer
	if err := pprof.Lookup("block").WriteTo(&rendered, 1); err != nil {
		t.Fatalf("render block profile: %v", err)
	}
	total := 0
	for _, line := range strings.Split(rendered.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[2] != "@" {
			continue
		}
		count, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		total += count
	}
	return total
}

// ApplyRates is what a server calls, and it must take effect only once per
// process so a second server cannot retune a profile an operator is already
// collecting. Asserted as a delta between two calls rather than against a
// fixed value, so the assertion holds whether or not this process has already
// spent the once — which is what makes it survive -count>1.
func TestApplyRatesTakesEffectOncePerProcess(t *testing.T) {
	mutexBefore := runtime.SetMutexProfileFraction(-1)
	t.Cleanup(func() {
		runtime.SetMutexProfileFraction(mutexBefore)
		runtime.SetBlockProfileRate(0)
	})

	ApplyRates(RuntimeRates{MutexProfileFraction: 3, BlockProfileRate: 1})
	settled := runtime.SetMutexProfileFraction(-1)

	ApplyRates(RuntimeRates{MutexProfileFraction: 9999, BlockProfileRate: 9999})
	if again := runtime.SetMutexProfileFraction(-1); again != settled {
		t.Fatalf("a second ApplyRates retuned the fraction %d -> %d", settled, again)
	}
}
