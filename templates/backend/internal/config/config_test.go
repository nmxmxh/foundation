package config

import "testing"

// config_test.go is foundation-owned and must compile against every project's
// config.go, so it asserts nothing about the loader's shape. Shape-specific
// tests live in config_security_test.go (create-mode).
func TestConfigScaffoldPackageCompiles(t *testing.T) {
	// This smoke test keeps configuration code in the unit-test set without
	// assuming project-owned loaders expose the same return shape or helpers.
}
