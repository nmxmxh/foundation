package load

import (
	"os"
	"testing"
)

// TestMain keeps helper tests lightweight while heavy load tests remain opt-in.
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
