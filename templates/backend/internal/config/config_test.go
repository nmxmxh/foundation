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
