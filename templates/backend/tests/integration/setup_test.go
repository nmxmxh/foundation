package integration

import (
	"flag"
	"os"
	"testing"

	"{{MODULE_PATH}}/tests/testutil"
)

// TestMain exports scaffolded integration defaults before config is loaded.
func TestMain(m *testing.M) {
	flag.Parse()
	testutil.ApplyTestEnvDefaults()
	os.Exit(m.Run())
}
