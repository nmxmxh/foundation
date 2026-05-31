//go:build integration

package integration

import (
	"testing"

	"{{MODULE_PATH}}/tests/testutil"
)

func setupTestWithDB(t *testing.T) *testutil.RealTestEnv {
	t.Helper()
	return testutil.SetupRealTestEnv(t)
}
