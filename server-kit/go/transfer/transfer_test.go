package transfer

import (
	"errors"
	"testing"
	"time"
)

func TestCanTransition_ForwardPipeline(t *testing.T) {
	t.Parallel()
	// Functional (black-box) coverage of the legal forward edges.
	cases := []struct {
		from, to Phase
		want     bool
	}{
		{PhasePending, PhaseUploading, true},
		{PhaseUploading, PhaseStaged, true},
		{PhaseStaged, PhaseProcessing, true},
		{PhaseProcessing, PhaseReady, true},
		// Skipping a forward stage is illegal.
		{PhasePending, PhaseStaged, false},
		{PhasePending, PhaseProcessing, false},
		{PhasePending, PhaseReady, false},
		{PhaseUploading, PhaseProcessing, false},
		{PhaseUploading, PhaseReady, false},
		{PhaseStaged, PhaseReady, false},
		// Backwards is illegal.
		{PhaseUploading, PhasePending, false},
		{PhaseProcessing, PhaseStaged, false},
	}
	for _, tc := range cases {
		if got := CanTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("CanTransition(%s,%s)=%v want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestCanTransition_FailureReachableFromAnyNonTerminal(t *testing.T) {
	t.Parallel()
	nonTerminal := []Phase{PhasePending, PhaseUploading, PhaseStaged, PhaseProcessing}
	for _, from := range nonTerminal {
		for _, to := range []Phase{PhaseFailed, PhaseAborted} {
			if !CanTransition(from, to) {
				t.Errorf("CanTransition(%s,%s)=false want true", from, to)
			}
		}
	}
}

func TestCanTransition_TerminalIsSink(t *testing.T) {
	t.Parallel()
	for _, from := range []Phase{PhaseReady, PhaseFailed, PhaseAborted} {
		for _, to := range []Phase{PhaseUploading, PhaseReady, PhaseFailed, PhaseAborted} {
			if CanTransition(from, to) {
				t.Errorf("CanTransition(terminal %s,%s)=true want false", from, to)
			}
		}
	}
}

func TestCanTransition_SelfAndUnknownRejected(t *testing.T) {
	t.Parallel()
	if CanTransition(PhaseUploading, PhaseUploading) {
		t.Error("self-transition must be rejected")
	}
	if CanTransition(Phase("bogus"), PhaseUploading) {
		t.Error("unknown from-phase must be rejected")
	}
	if CanTransition(PhaseUploading, Phase("bogus")) {
		t.Error("unknown to-phase must be rejected")
	}
}

func TestIsTerminalAndValid(t *testing.T) {
	t.Parallel()
	for _, p := range []Phase{PhaseReady, PhaseFailed, PhaseAborted} {
		if !IsTerminal(p) {
			t.Errorf("IsTerminal(%s)=false want true", p)
		}
	}
	for _, p := range []Phase{PhasePending, PhaseUploading, PhaseStaged, PhaseProcessing} {
		if IsTerminal(p) {
			t.Errorf("IsTerminal(%s)=true want false", p)
		}
	}
	if IsValidPhase(Phase("nope")) {
		t.Error("IsValidPhase must reject unknown phase")
	}
}

func TestUpdateFraction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		u    Update
		want float64
	}{
		{"unknown total is zero", Update{BytesDone: 10, BytesTotal: 0}, 0},
		{"half", Update{BytesDone: 50, BytesTotal: 100}, 0.5},
		{"over total clamps to 1", Update{BytesDone: 150, BytesTotal: 100}, 1},
		{"ready is 1 even with unknown total", Update{Phase: PhaseReady, BytesTotal: 0}, 1},
		{"zero progress", Update{BytesDone: 0, BytesTotal: 100}, 0},
	}
	for _, tc := range cases {
		if got := tc.u.Fraction(); got != tc.want {
			t.Errorf("%s: Fraction()=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestUpdateTerminal(t *testing.T) {
	t.Parallel()
	if !(Update{Phase: PhaseReady}).Terminal() {
		t.Error("ready Update must be terminal")
	}
	if (Update{Phase: PhaseUploading}).Terminal() {
		t.Error("uploading Update must not be terminal")
	}
}

func TestValidateIdentifier(t *testing.T) {
	t.Parallel()
	if _, err := validateIdentifier("id", "  "); !errors.Is(err, ErrEmptyID) {
		t.Errorf("blank id: got %v want ErrEmptyID", err)
	}
	if _, err := validateIdentifier("correlation", ""); !errors.Is(err, ErrEmptyCorrelation) {
		t.Errorf("blank correlation: got %v want ErrEmptyCorrelation", err)
	}
	if _, err := validateIdentifier("id", "has:colon"); err == nil {
		t.Error("colon in identifier must be rejected")
	}
	if _, err := validateIdentifier("id", "has space"); err == nil {
		t.Error("whitespace in identifier must be rejected")
	}
	got, err := validateIdentifier("id", "  tx-123 ")
	if err != nil || got != "tx-123" {
		t.Errorf("trim: got (%q,%v) want (tx-123,nil)", got, err)
	}
}

// fixedClock returns a deterministic, monotonically advancing clock for tests.
func fixedClock() func() time.Time {
	base := time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	var n int64
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * time.Millisecond)
	}
}
