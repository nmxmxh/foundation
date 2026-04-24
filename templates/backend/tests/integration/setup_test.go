package integration

import (
	"context"
	"flag"
	"os"
	"testing"

	"{{MODULE_PATH}}/tests/testutil"
)

var (
	// testDB holds the shared database pool for integration tests.
	// Initialize in TestMain if a package needs a shared environment.
	testDB *testutil.RealTestEnv
)

// TestMain exports scaffolded integration defaults before config is loaded.
func TestMain(m *testing.M) {
	flag.Parse()
	testutil.ApplyTestEnvDefaults()
	os.Exit(m.Run())
}

// skipIfNoDatabase keeps older integration tests compatible with the managed helper.
func skipIfNoDatabase(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("database integration skipped in short mode")
	}
}

// setupTestWithDB creates a test environment with real database.
// Use this at the start of integration tests.
func setupTestWithDB(t *testing.T) *testutil.RealTestEnv {
	t.Helper()
	skipIfNoDatabase(t)
	return testutil.SetupRealTestEnv(t)
}

// setupTestWithMock creates a test environment with mock database.
// Use this for unit tests that don't need real database.
func setupTestWithMock(t *testing.T) *testutil.TestEnv {
	t.Helper()
	return testutil.SetupTestEnv(t)
}

// withCleanContext returns a fresh context for test operations.
func withCleanContext() context.Context {
	return context.Background()
}
