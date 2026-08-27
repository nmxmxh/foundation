package config

import "testing"

func TestConfigScaffoldPackageCompiles(t *testing.T) {
	// This smoke test keeps configuration code in the unit-test set without
	// assuming project-owned loaders expose the same return shape or helpers.
}

// TestProductionRejectsKnownWeakJWTSecrets guards the failure recorded in
// security_practices.md: production validation rejected an *empty* JWT_SECRET
// while the compose fallback guaranteed the value was never empty. A deploy
// that forgot to set the variable therefore passed validation and signed tokens
// with a key published in every repository built from Foundation.
//
// The property is that publication, not length, decides whether a secret is
// acceptable — so a 64-character literal must fail exactly like a placeholder.
func TestProductionRejectsKnownWeakJWTSecrets(t *testing.T) {
	for secret := range knownWeakJWTSecrets {
		t.Run(secret, func(t *testing.T) {
			cfg := &Config{
				Env:            "production",
				JWTSecret:      secret,
				AllowedOrigins: []string{"https://example.test"},
			}
			if err := cfg.validateSecurity(); err == nil {
				t.Fatalf("production accepted a known weak JWT_SECRET (%q); "+
					"a published signing key must not pass validation", secret)
			}
		})
	}
}

// A fresh secret must still pass, or the denylist would be a blanket refusal
// rather than a check for known values.
func TestProductionAcceptsUnknownJWTSecret(t *testing.T) {
	cfg := &Config{
		Env:            "production",
		JWTSecret:      "a-secret-that-is-not-on-the-denylist",
		AllowedOrigins: []string{"https://example.test"},
	}
	if err := cfg.validateSecurity(); err != nil {
		t.Fatalf("production rejected a secret that is not published: %v", err)
	}
}

// TestEnvSpellingCannotDowngradePosture guards the fail-open this file's
// production checks all depend on. RequireAuth, ProtectOperationalEndpoints,
// the weak-secret rejection and the CORS wildcard rejection are each decided
// by an exact comparison against "production". Before normalization, an
// APP_ENV of "Production" or "prod" satisfied none of them while the logger —
// which compares with EqualFold — reported the process as production. The
// result was a server that looked deployed and ran open.
func TestEnvSpellingCannotDowngradePosture(t *testing.T) {
	for _, spelling := range []string{"production", "Production", "PRODUCTION", " production ", "prod"} {
		t.Run(spelling, func(t *testing.T) {
			normalized, err := normalizeEnv(spelling)
			if err != nil {
				t.Fatalf("normalizeEnv(%q) returned error: %v", spelling, err)
			}
			if normalized != "production" {
				t.Fatalf("normalizeEnv(%q) = %q, want production", spelling, normalized)
			}
			if !(&Config{Env: spelling}).IsProduction() {
				t.Fatalf("Config{Env: %q}.IsProduction() = false, want true", spelling)
			}
		})
	}
}

// TestUnrecognizedEnvIsRefused pins the fail-closed direction: an APP_ENV
// nobody recognizes must stop the boot, not quietly resolve to development.
func TestUnrecognizedEnvIsRefused(t *testing.T) {
	for _, spelling := range []string{"prodution", "stage", "live", "PROD_1", "developement"} {
		t.Run(spelling, func(t *testing.T) {
			if _, err := normalizeEnv(spelling); err == nil {
				t.Fatalf("normalizeEnv(%q) succeeded; unrecognized environments must be refused", spelling)
			}
		})
	}
}
