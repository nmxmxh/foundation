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

var terminalStates = map[string]struct{}{
	"requested": {},
	"success":   {},
	"failed":    {},
	"ack":       {},
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
	if _, ok := terminalStates[terminal]; !ok {
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
	if _, ok := terminalStates[terminal]; !ok {
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
	if err := ValidateEventType(et); err != nil {
		return ""
	}
	lastColon := strings.LastIndexByte(et, ':')
	if lastColon <= 0 || lastColon == len(et)-1 {
		return ""
	}
	return et[lastColon+1:]
}

func EnsureTerminalState(eventType, terminal string) string {
	terminal = strings.TrimSpace(terminal)
	if _, ok := terminalStates[terminal]; !ok {
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
			if _, isTerminal := terminalStates[suffix]; isTerminal {
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
