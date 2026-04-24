package profiling

import (
	"net/http"
	"net/http/pprof"
	"strings"
)

type Config struct {
	Enabled         bool
	AdminPathPrefix string
	Authorize       func(*http.Request) bool
}

func Handler(cfg Config) http.Handler {
	prefix := strings.TrimRight(cfg.AdminPathPrefix, "/")
	if prefix == "" {
		prefix = "/debug/pprof"
	}
	mux := http.NewServeMux()
	mux.HandleFunc(prefix+"/", pprof.Index)
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
