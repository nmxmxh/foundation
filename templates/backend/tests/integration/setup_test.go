package integration

import (
	"context"
	"log"
	"os"
	"testing"

	"{{MODULE_PATH}}/tests/testutil"
)

var (
	// testDB holds the shared database pool for integration tests.
	// Initialize in TestMain to share across all tests in the package.
	testDB *testutil.RealTestEnv
)

// TestMain runs before all tests in this package.
// It sets up the test database connection and runs migrations if needed.
func TestMain(m *testing.M) {
	// Skip setup if running short tests
	if testing.Short() {
		os.Exit(m.Run())
	}

	// Verify we can connect to the test database
	dbURL := testutil.ResolveTestDatabaseURL()
	if dbURL == "" {
		log.Println("TEST_DB_* or DB_* environment variables not set, skipping integration tests")
		os.Exit(0)
	}

	// Run all tests
	code := m.Run()
	os.Exit(code)
}

// skipIfNoDatabase skips the test if no database connection is available.
func skipIfNoDatabase(t *testing.T) {
	t.Helper()
	dbURL := testutil.ResolveTestDatabaseURL()
	if dbURL == "" {
		t.Skip("database connection not available")
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
