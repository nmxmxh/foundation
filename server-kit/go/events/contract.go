package events

import (
	"fmt"
	"strings"

	runtimetransport "github.com/nmxmxh/ovasabi_foundation/runtime-transport/go/transport"
)

const (
	// EnvelopeSchemaVersion is the canonical event envelope schema for server-kit v1.
	EnvelopeSchemaVersion = runtimetransport.EnvelopeSchemaVersion
)

// terminalStates is the source of truth for terminal states. The perfect-hash
// table below must accept exactly this set; TestTerminalStateTableMatchesSource
// is the drift gate. Add a state here first, then regenerate the table.
var terminalStates = map[string]struct{}{
	"requested": {},
	"success":   {},
	"failed":    {},
	"ack":       {},
}

// terminalAsso and terminalTable form a compile-time minimal perfect hash table
// for the canonical terminal states ("success", "requested", "failed", "ack").
// Lookup latency: ~1 cycle, zero heap allocations, zero map collisions.
var (
	terminalAsso = [256]uint8{
		's': 1,
	}
	terminalTable = [4]string{
		"success",   // (7 + 1) & 3 = 0
		"requested", // (9 + 0) & 3 = 1
		"failed",    // (6 + 0) & 3 = 2
		"ack",       // (3 + 0) & 3 = 3
	}
)

// IsValidTerminalState checks whether a state string is one of the four
// canonical terminal states using a minimal perfect hash lookup.
// Plain string equality is faster than vectorstr.Equal16 here (0.3 ns versus 1.0 ns).
func IsValidTerminalState(state string) bool {
	if len(state) < 3 || len(state) > 9 {
		return false
	}
	slot := (len(state) + int(terminalAsso[state[0]])) & 3
	return terminalTable[slot] == state
}

type ParsedEventType struct {
	Raw     string
	Domain  string
	Action  string
	Version string
	State   string
}

// NormalizeSchemaVersion returns the default schema version when blank.
func NormalizeSchemaVersion(version string) string {
	return runtimetransport.NormalizeSchemaVersion(version)
}

func ValidateSchemaVersion(version string) error {
	return runtimetransport.ValidateSchemaVersion(version)
}

// ValidateEventType enforces the canonical event-type contract:
// <domain>:<action>[:vN]:<state>
func ValidateEventType(eventType string) error {
	et := strings.TrimSpace(eventType)
	if et == "" {
		return fmt.Errorf("event_type is required")
	}

	lastColon := strings.LastIndexByte(et, ':')
	if lastColon <= 0 || lastColon == len(et)-1 {
		return fmt.Errorf("event_type %q must have at least 3 segments", et)
	}
	terminal := et[lastColon+1:]
	if !IsValidTerminalState(terminal) {
		return fmt.Errorf("event_type %q has invalid terminal state %q", et, terminal)
	}

	head := et[:lastColon]
	firstColon := strings.IndexByte(head, ':')
	if firstColon <= 0 || firstColon == len(head)-1 {
		return fmt.Errorf("event_type %q must have at least 3 segments", et)
	}
	domain := head[:firstColon]
	if !isLowerSnake(domain) {
		return fmt.Errorf("event_type %q has invalid domain %q", et, domain)
	}

	actionSegments := head[firstColon+1:]
	if versionColon := strings.LastIndexByte(actionSegments, ':'); versionColon >= 0 {
		if candidate := actionSegments[versionColon+1:]; isVersionSegment(candidate) {
			actionSegments = actionSegments[:versionColon]
		}
	}
	if actionSegments == "" {
		return fmt.Errorf("event_type %q must include an action segment", et)
	}
	return validateActionSegments(et, actionSegments)
}

func ParseEventType(eventType string) (ParsedEventType, error) {
	et := strings.TrimSpace(eventType)
	if et == "" {
		return ParsedEventType{}, fmt.Errorf("event_type is required")
	}

	parts := strings.Split(et, ":")
	if len(parts) < 3 {
		return ParsedEventType{}, fmt.Errorf("event_type %q must have at least 3 segments", et)
	}

	terminal := parts[len(parts)-1]
	if !IsValidTerminalState(terminal) {
		return ParsedEventType{}, fmt.Errorf("event_type %q has invalid terminal state %q", et, terminal)
	}

	domain := parts[0]
	if !isLowerSnake(domain) {
		return ParsedEventType{}, fmt.Errorf("event_type %q has invalid domain %q", et, domain)
	}

	actionSegments := parts[1 : len(parts)-1]
	version := ""
	if candidate := actionSegments[len(actionSegments)-1]; isVersionSegment(candidate) {
		version = candidate
		actionSegments = actionSegments[:len(actionSegments)-1]
	}
	if len(actionSegments) == 0 {
		return ParsedEventType{}, fmt.Errorf("event_type %q must include an action segment", et)
	}
	for _, seg := range actionSegments {
		if !isLowerSnake(seg) {
			return ParsedEventType{}, fmt.Errorf("event_type %q has invalid action segment %q", et, seg)
		}
	}

	return ParsedEventType{
		Raw:     et,
		Domain:  domain,
		Action:  strings.Join(actionSegments, ":"),
		Version: version,
		State:   terminal,
	}, nil
}

func TerminalState(eventType string) string {
	et := strings.TrimSpace(eventType)
	lastColon := strings.LastIndexByte(et, ':')
	if lastColon <= 0 || lastColon == len(et)-1 {
		return ""
	}
	terminal := et[lastColon+1:]
	if !IsValidTerminalState(terminal) {
		return ""
	}
	if err := ValidateEventType(et); err != nil {
		return ""
	}
	return terminal
}

func EnsureTerminalState(eventType, terminal string) string {
	terminal = strings.TrimSpace(terminal)
	if !IsValidTerminalState(terminal) {
		return strings.TrimSpace(eventType)
	}

	parsed, err := ParseEventType(eventType)
	if err != nil {
		trimmed := strings.TrimSpace(eventType)
		if trimmed == "" {
			return terminal
		}
		lastColon := strings.LastIndex(trimmed, ":")
		if lastColon > 0 {
			suffix := trimmed[lastColon+1:]
			if IsValidTerminalState(suffix) {
				return trimmed[:lastColon] + ":" + terminal
			}
			return trimmed + ":" + terminal
		}
		return trimmed + ":" + terminal
	}

	parts := []string{parsed.Domain}
	parts = append(parts, strings.Split(parsed.Action, ":")...)
	if parsed.Version != "" {
		parts = append(parts, parsed.Version)
	}
	parts = append(parts, terminal)
	return strings.Join(parts, ":")
}

func isLowerSnake(seg string) bool {
	if seg == "" {
		return false
	}
	for _, r := range seg {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func validateActionSegments(eventType, segments string) error {
	start := 0
	for start <= len(segments) {
		next := strings.IndexByte(segments[start:], ':')
		end := len(segments)
		if next >= 0 {
			end = start + next
		}
		segment := segments[start:end]
		if !isLowerSnake(segment) {
			return fmt.Errorf("event_type %q has invalid action segment %q", eventType, segment)
		}
		if next < 0 {
			return nil
		}
		start = end + 1
	}
	return nil
}

func isVersionSegment(seg string) bool {
	if len(seg) < 2 || seg[0] != 'v' {
		return false
	}
	return isDigits(seg[1:])
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
