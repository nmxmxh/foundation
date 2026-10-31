package events

import (
	"strings"
	"testing"
)

// TestTerminalStateTableMatchesSource checks the hand-written perfect hash
// against terminalStates: every state resolves, and nothing else does.
func TestTerminalStateTableMatchesSource(t *testing.T) {
	for state := range terminalStates {
		if !IsValidTerminalState(state) {
			t.Fatalf("terminal state %q missing from the perfect-hash table", state)
		}
	}
	for _, entry := range terminalTable {
		if _, ok := terminalStates[entry]; !ok {
			t.Fatalf("perfect-hash table holds %q, which is not a terminal state", entry)
		}
	}
	if len(terminalTable) != len(terminalStates) {
		t.Fatalf("table size %d != source size %d", len(terminalTable), len(terminalStates))
	}
	near := []string{"", "Success", "succes", "successs", "requeste", "fail", "acks", "ACK", "faile d"}
	for state := range terminalStates {
		near = append(near, strings.ToUpper(state), state+" ", " "+state, state[1:])
	}
	for _, candidate := range near {
		_, want := terminalStates[candidate]
		if IsValidTerminalState(candidate) != want {
			t.Fatalf("IsValidTerminalState(%q) = %v, want %v", candidate, !want, want)
		}
	}
}
