package bulk

import (
	"context"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/kernellane"
)

// TestDetectPlatformCapabilitiesProbedReflectsRealProbes verifies the probed
// detector reports exactly what the kernellane accelerators advertise, with
// explanatory notes — so the lane planner is fed verified capability, not an
// OS-name guess.
func TestDetectPlatformCapabilitiesProbedReflectsRealProbes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	caps := DetectPlatformCapabilitiesProbed(ctx)

	if caps.OS == "" {
		t.Fatal("OS not populated")
	}
	if caps.ZeroCopyAvailable != kernellane.ZeroCopyFileSupported() {
		t.Fatalf("zero-copy capability %v disagrees with kernellane probe", caps.ZeroCopyAvailable)
	}
	if caps.MPTCPAvailable != kernellane.MultipathTCPSupported(ctx) {
		t.Fatalf("mptcp capability %v disagrees with kernellane probe", caps.MPTCPAvailable)
	}
	if caps.Notes["zero_copy"] == "" || caps.Notes["mptcp"] == "" {
		t.Fatalf("expected explanatory notes, got %#v", caps.Notes)
	}
}

func TestCapabilityNotesDescribeBothStates(t *testing.T) {
	if zeroCopyNote(true) == zeroCopyNote(false) || mptcpNote(true) == mptcpNote(false) {
		t.Fatal("note helpers must distinguish available from fallback")
	}
	for _, s := range []string{zeroCopyNote(true), zeroCopyNote(false), mptcpNote(true), mptcpNote(false)} {
		if s == "" {
			t.Fatal("note must not be empty")
		}
	}
}
