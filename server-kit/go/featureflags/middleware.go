package featureflags

import (
	"context"
	"net/http"
)

type contextKey string

const (
	evalContextKey contextKey = "feature_flags_eval_context"
	managerKey     contextKey = "feature_flags_manager"
)

// ContextWithEvaluation adds evaluation context to the request context.
func ContextWithEvaluation(ctx context.Context, evalCtx *EvaluationContext) context.Context {
	return context.WithValue(ctx, evalContextKey, evalCtx)
}

// EvaluationFromContext retrieves evaluation context from the request context.
func EvaluationFromContext(ctx context.Context) *EvaluationContext {
	if v := ctx.Value(evalContextKey); v != nil {
		return v.(*EvaluationContext)
	}
	return &EvaluationContext{}
}

// ContextWithManager adds the manager to the request context.
func ContextWithManager(ctx context.Context, m *Manager) context.Context {
	return context.WithValue(ctx, managerKey, m)
}

// ManagerFromContext retrieves the manager from the request context.
func ManagerFromContext(ctx context.Context) *Manager {
	if v := ctx.Value(managerKey); v != nil {
		return v.(*Manager)
	}
	return nil
}

// IsEnabledFromContext checks if a flag is enabled using context values.
func IsEnabledFromContext(ctx context.Context, name string, opts ...Option) bool {
	m := ManagerFromContext(ctx)
	if m == nil {
		return false
	}

	evalCtx := EvaluationFromContext(ctx)
	allOpts := make([]Option, 0, len(opts)+3)

	if evalCtx.UserID != "" {
		allOpts = append(allOpts, WithUser(evalCtx.UserID))
	}
	if evalCtx.OrgID != "" {
		allOpts = append(allOpts, WithOrg(evalCtx.OrgID))
	}
	if evalCtx.Environment != "" {
		allOpts = append(allOpts, WithEnvironment(evalCtx.Environment))
	}

	allOpts = append(allOpts, opts...)
	return m.IsEnabled(ctx, name, allOpts...)
}

// Middleware adds the feature flag manager to the request context.
func Middleware(m *Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := ContextWithManager(r.Context(), m)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserMiddleware extracts user information and adds it to evaluation context.
// userIDExtractor is a function that extracts the user ID from the request.
func UserMiddleware(userIDExtractor func(*http.Request) string, orgIDExtractor func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			evalCtx := &EvaluationContext{}

			if userIDExtractor != nil {
				evalCtx.UserID = userIDExtractor(r)
			}
			if orgIDExtractor != nil {
				evalCtx.OrgID = orgIDExtractor(r)
			}

			ctx := ContextWithEvaluation(r.Context(), evalCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireFlag returns a middleware that only allows requests if the flag is enabled.
func RequireFlag(m *Manager, flagName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			evalCtx := EvaluationFromContext(r.Context())
			opts := []Option{}
			if evalCtx.UserID != "" {
				opts = append(opts, WithUser(evalCtx.UserID))
			}
			if evalCtx.OrgID != "" {
				opts = append(opts, WithOrg(evalCtx.OrgID))
			}

			if !m.IsEnabled(r.Context(), flagName, opts...) {
				http.Error(w, "Feature not available", http.StatusNotFound)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
