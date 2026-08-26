// Package profiling exposes Go's pprof surface behind an explicit,
// environment-gated handler.
//
// Mount it where the process already serves HTTP, arming the sampling rates
// once at startup:
//
//	cfg := profiling.FromEnv(os.Getenv)
//	profiling.ApplyRates(cfg.Rates)
//	mux.Handle("/debug/pprof/", profiling.Handler(cfg))
//
// Or set httpserver.Config.EnableProfiling and let httpserver mount it behind
// operational-endpoint protection and arm the rates for you.
//
// The handler answers 404 while Config.Enabled is false, so mounting it
// unconditionally is safe; authorization stays the caller's responsibility
// through Config.Authorize or the surrounding middleware.
//
// Arming matters as much as mounting. The mutex and block profiles return an
// empty result rather than an error until their rates are set, so a surface
// that is mounted but unarmed answers a lock-contention question with a
// successful, empty profile.
package profiling

import (
	"net/http"
	"net/http/pprof"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Environment variables read by FromEnv.
const (
	// EnvEnabled turns the profile surface on. Accepted, case-insensitively
	// and after trimming: 1, t, true, y, yes, on. Anything else — including
	// empty and unset — keeps it disabled, so the default is off and a
	// typo fails closed rather than exposing profiles.
	EnvEnabled = "ENABLE_PPROF"

	// EnvPrefix overrides the URL prefix. Empty keeps /debug/pprof.
	EnvPrefix = "PPROF_PATH_PREFIX"

	// EnvMutexFraction and EnvBlockRate override the sampling rates that
	// make the mutex and block profiles report anything. See RuntimeRates.
	EnvMutexFraction = "PPROF_MUTEX_PROFILE_FRACTION"
	EnvBlockRate     = "PPROF_BLOCK_PROFILE_RATE"
)

// RuntimeRates carries the two process-global sampling rates that the mutex
// and block profiles depend on.
//
// Both default to zero in the Go runtime, and at zero those two profiles
// return a valid, empty result — not an error. Anyone diagnosing lock
// contention therefore gets a successful-looking answer that says nothing,
// which is worse than a failure. Mounting the pprof surface is not enough on
// its own; these have to be armed as well.
type RuntimeRates struct {
	// MutexProfileFraction reports one of every N contention events. Zero
	// leaves the mutex profile empty.
	MutexProfileFraction int
	// BlockProfileRate samples one blocking event per N nanoseconds spent
	// blocked. Zero leaves the block profile empty.
	BlockProfileRate int
}

// SampledRates are the rates applied when profiling is switched on without
// explicit ones: frequent enough to localize contention within a few minutes,
// sparse enough to leave under a percent of throughput on the floor. They are
// installed only behind an explicit enablement, never by default.
func SampledRates() RuntimeRates {
	return RuntimeRates{MutexProfileFraction: 100, BlockProfileRate: 10_000}
}

// ratesFromEnv reads the rate overrides, falling back to SampledRates for any
// value that is absent or unparseable. A negative value disables its profile,
// which is how an operator asks for CPU and heap profiles without paying for
// contention sampling.
func ratesFromEnv(getenv func(string) string) RuntimeRates {
	rates := SampledRates()
	if value, ok := parseInt(getenv(EnvMutexFraction)); ok {
		rates.MutexProfileFraction = max(value, 0)
	}
	if value, ok := parseInt(getenv(EnvBlockRate)); ok {
		rates.BlockProfileRate = max(value, 0)
	}
	return rates
}

func parseInt(value string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// applyRatesOnce keeps repeated server construction in one process from
// stacking rate changes, so the first enablement wins and a second server
// cannot silently retune a profile an operator is already collecting.
var applyRatesOnce sync.Once

// ApplyRates installs the sampling rates process-wide. It is safe to call
// repeatedly; only the first call in a process takes effect.
func ApplyRates(rates RuntimeRates) {
	applyRatesOnce.Do(func() { applyRates(rates) })
}

func applyRates(rates RuntimeRates) {
	if rates.MutexProfileFraction > 0 {
		runtime.SetMutexProfileFraction(rates.MutexProfileFraction)
	}
	if rates.BlockProfileRate > 0 {
		runtime.SetBlockProfileRate(rates.BlockProfileRate)
	}
}

type Config struct {
	Enabled         bool
	AdminPathPrefix string
	Authorize       func(*http.Request) bool

	// Rates are the sampling rates to arm alongside the surface. Handler
	// does not install them — it has no side effects and is called per
	// request — so a caller mounting this directly pairs it with one
	// ApplyRates(cfg.Rates) at startup.
	Rates RuntimeRates
}

// DefaultPathPrefix is where the pprof surface mounts unless overridden.
const DefaultPathPrefix = "/debug/pprof"

// FromEnv builds a Config from environment lookups, so deployments can flip
// profiling on for the duration of an investigation without a rebuild.
func FromEnv(getenv func(string) string) Config {
	cfg := Config{
		Enabled:         truthy(getenv(EnvEnabled)),
		AdminPathPrefix: strings.TrimSpace(getenv(EnvPrefix)),
	}
	// Rates stay zero while disabled, so a config built from an environment
	// that never asked for profiling cannot arm process-global sampling.
	if cfg.Enabled {
		cfg.Rates = ratesFromEnv(getenv)
	}
	return cfg
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func Handler(cfg Config) http.Handler {
	prefix := strings.TrimRight(cfg.AdminPathPrefix, "/")
	if prefix == "" {
		prefix = DefaultPathPrefix
	}
	mux := http.NewServeMux()
	mux.HandleFunc(prefix+"/", namedProfileHandler(prefix))
	mux.HandleFunc(prefix+"/cmdline", pprof.Cmdline)
	mux.HandleFunc(prefix+"/profile", pprof.Profile)
	mux.HandleFunc(prefix+"/symbol", pprof.Symbol)
	mux.HandleFunc(prefix+"/trace", pprof.Trace)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Enabled {
			http.NotFound(w, r)
			return
		}
		if cfg.Authorize != nil && !cfg.Authorize(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// namedProfileHandler routes the runtime's named profiles — heap, goroutine,
// mutex, block, allocs, threadcreate — and falls back to the index page.
//
// pprof.Index cannot do this itself under a custom prefix: it dispatches a
// named profile only when the request path starts with the literal
// "/debug/pprof/", and otherwise renders the index. So with AdminPathPrefix
// set, a request for /admin/profile/heap returned 200 with the index HTML in
// place of the profile — a success status carrying the wrong content type,
// which a collector reports as a parse failure rather than a routing bug.
// Dispatching by name here is what makes the prefix genuinely configurable.
func namedProfileHandler(prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, prefix+"/")
		if name == "" || strings.Contains(name, "/") {
			pprof.Index(w, r)
			return
		}
		// An unknown name answers 404 through pprof's own handler, which
		// keeps the error shape identical to the default mount.
		pprof.Handler(name).ServeHTTP(w, r)
	}
}
