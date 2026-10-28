package transport

import (
	"strconv"
	"testing"
)

func TestPerfectRouteIndex_Resolve(t *testing.T) {
	routes := []Route{
		{Method: "POST", Path: "/media/upload", EventType: "media:upload:v1:requested"},
		{Method: "GET", Path: "/media/probe", EventType: "media:probe:requested"},
		{Method: "POST", Path: "/user/create", EventType: "user:create:v1:requested"},
	}

	// Handcraft a minimal perfect hash for the 3 routes:
	// "media:upload:v1:requested" (len 25)
	// "media:probe:requested" (len 21)
	// "user:create:v1:requested" (len 24)
	// Let's use mask 3.
	// 25 & 3 = 1
	// 21 & 3 = 1 -> with asso['p']=1: 22 & 3 = 2
	// 24 & 3 = 0
	var asso [256]uint8
	asso['p'] = 1 // 'media:probe' 7th char or first char

	// Slot 0: route 2 ("user:create:v1:requested")
	// Slot 1: route 0 ("media:upload:v1:requested")
	// Slot 2: route 1 ("media:probe:requested")
	slots := []int{2, 0, 1, -1}

	index := NewPerfectRouteIndex(routes, 3, asso, 0, -1, slots)
	if index == nil {
		t.Fatal("expected non-nil PerfectRouteIndex")
	}

	for _, want := range routes {
		got := index.Resolve(want.EventType)
		if got == nil {
			t.Fatalf("failed to resolve %q", want.EventType)
		}
		if got.Path != want.Path || got.Method != want.Method {
			t.Fatalf("Resolve(%q) = %+v, want %+v", want.EventType, got, want)
		}
	}

	// Missing route
	if got := index.Resolve("unknown:event:v1:requested"); got != nil {
		t.Fatalf("expected nil for unknown event, got %+v", got)
	}
	if got := index.Resolve(""); got != nil {
		t.Fatalf("expected nil for empty event, got %+v", got)
	}
	if (*PerfectRouteIndex)(nil).Resolve("media:upload:v1:requested") != nil {
		t.Fatal("nil index must resolve nil")
	}

	// Fallback path when slots empty
	noSlots := &PerfectRouteIndex{
		fallback: NewRouteIndex(routes),
	}
	if got := noSlots.Resolve("user:create:v1:requested"); got == nil {
		t.Fatal("expected fallback resolution")
	}
	if got := noSlots.Resolve("unknown"); got != nil {
		t.Fatal("expected nil for unknown in fallback")
	}

	// Nil fallback
	nilFb := &PerfectRouteIndex{}
	if got := nilFb.Resolve("unknown"); got != nil {
		t.Fatal("expected nil with nil fallback")
	}

	// Edge positions (e.g. out of range or negative pos)
	edgeIndex := NewPerfectRouteIndex(routes, 3, asso, -100, 100, slots)
	_ = edgeIndex.Resolve("media:upload:v1:requested")
}

func BenchmarkRouteIndex_Map_16(b *testing.B) {
	routes := make([]Route, 16)
	for i := range routes {
		routes[i] = Route{
			Method:    "POST",
			Path:      "/runtime/dispatch",
			EventType: "runtime:event_" + strconv.Itoa(i) + ":v1:requested",
		}
	}
	index := NewRouteIndex(routes)
	target := routes[8].EventType
	b.ReportAllocs()
	for b.Loop() {
		if index.Resolve(target) == nil {
			b.Fatal("route not resolved")
		}
	}
}
