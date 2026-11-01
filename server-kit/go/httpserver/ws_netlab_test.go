//go:build netlab

package httpserver

// The network lab measures server-to-browser runtime frames over a shaped link.
// tooling/scripts/ws_netlab.sh runs it in a Linux container where tc netem shapes
// loopback, so the kernel TCP stack applies, including slow start after idle.
// It is not part of ordinary test runs.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	kitcompress "github.com/nmxmxh/ovasabi_foundation/server-kit/go/compress"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/events"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"
)

// netlabSizes are projection pushes of 12, 50, 400, and 3200 records (about 1.3 KB to 275 KB).
var netlabSizes = []int{12, 50, 400, 3200}

type netlabRecord struct {
	ID     string `json:"record_id"`
	Name   string `json:"name"`
	Price  int    `json:"price_minor"`
	Status string `json:"status"`
}

func netlabRecords(n int) []netlabRecord {
	out := make([]netlabRecord, n)
	for i := range out {
		out[i] = netlabRecord{fmt.Sprintf("rec_%06d", i*7919%999983), fmt.Sprintf("Dish %d", i%400), i * 137 % 900000, []string{"available", "sold_out"}[i%2]}
	}
	return out
}

type netlabResult struct {
	Profile    string  `json:"profile"`
	Records    int     `json:"records"`
	Mode       string  `json:"mode"`
	Idle       bool    `json:"after_idle"`
	WireBytes  int     `json:"wire_bytes"`
	RawBytes   int     `json:"raw_bytes"`
	MedianMS   float64 `json:"median_ms"`
	P90MS      float64 `json:"p90_ms"`
	Trials     int     `json:"trials"`
	IdleMillis int64   `json:"idle_ms"`
	Congestion string  `json:"congestion"`
	SlowStart  string  `json:"slow_start_after_idle"`
	GzipLevel  int     `json:"gzip_level"`
}

// netlabSysctl reads a TCP sysctl of this network namespace for the result rows.
func netlabSysctl(name string) string {
	data, err := os.ReadFile("/proc/sys/net/ipv4/" + name)
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

func netlabEnvInt(name string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return fallback
}

// netlabRoundTrip sends one small request and reads the full response.
// It returns the elapsed time and the response size on the wire.
func netlabRoundTrip(t *testing.T, conn *websocket.Conn, eventType string) (time.Duration, int, int) {
	t.Helper()
	request, err := requested(eventType, extension.Object{}).ToBinary()
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	start := time.Now()
	if err := conn.WriteMessage(websocket.BinaryMessage, request); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, data, err := conn.ReadMessage()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	raw := data
	if kitcompress.IsRuntimeFrame(data) {
		if raw, err = kitcompress.DecodeRuntimeFrame(data, 0); err != nil {
			t.Fatalf("decode frame: %v", err)
		}
	}
	env, err := events.FromBinary(raw)
	if err != nil || env.EventType != eventType[:len(eventType)-len("requested")]+"success" {
		t.Fatalf("response = (%q, %v)", env.EventType, err)
	}
	return elapsed, len(data), len(raw)
}

func netlabPercentile(d []time.Duration, p float64) float64 {
	sorted := append([]time.Duration(nil), d...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(p * float64(len(sorted)-1))
	return float64(sorted[idx].Microseconds()) / 1000
}

func TestWSNetlab(t *testing.T) {
	handlers := map[string]wsHandler{}
	for _, n := range netlabSizes {
		records := netlabRecords(n)
		handlers[fmt.Sprintf("netlab:rows_%d:v1:requested", n)] = func(context.Context, extension.Object) (any, error) {
			return map[string]any{"records": records}, nil
		}
	}
	srv, s := newWSTestServer(t, handlers)
	// Unset keeps the server default, so a plain run measures what production sends.
	s.wsFrameGzipLevel = netlabEnvInt("NETLAB_GZIP_LEVEL", s.wsFrameGzipLevel)
	modes := map[string]*websocket.Conn{
		"raw":    dialWS(t, srv, "format=binary"),
		"framed": dialWS(t, srv, "format=binary&compression=gzip,deflate"),
	}
	for _, conn := range modes {
		if _, _, err := conn.ReadMessage(); err != nil { // ack
			t.Fatalf("read ack: %v", err)
		}
	}
	trials := netlabEnvInt("NETLAB_TRIALS", 9)
	idle := time.Duration(netlabEnvInt("NETLAB_IDLE_MS", 1500)) * time.Millisecond
	profile := os.Getenv("NETLAB_PROFILE")
	out := json.NewEncoder(os.Stdout)

	for _, n := range netlabSizes {
		eventType := fmt.Sprintf("netlab:rows_%d:v1:requested", n)
		for _, afterIdle := range []bool{false, true} {
			samples := map[string][]time.Duration{}
			wire := map[string]int{}
			raw := 0
			for range trials {
				// Interleave the modes so drift in the link affects both equally.
				for _, mode := range []string{"raw", "framed"} {
					if afterIdle {
						time.Sleep(idle) // longer than the RTO, so Linux restarts slow start
					} else {
						_, _, _ = netlabRoundTrip(t, modes[mode], eventType) // warm the window
					}
					d, w, r := netlabRoundTrip(t, modes[mode], eventType)
					samples[mode] = append(samples[mode], d)
					wire[mode], raw = w, r
				}
			}
			for _, mode := range []string{"raw", "framed"} {
				if err := out.Encode(netlabResult{
					Profile: profile, Records: n, Mode: mode, Idle: afterIdle, WireBytes: wire[mode], RawBytes: raw,
					MedianMS: netlabPercentile(samples[mode], 0.5), P90MS: netlabPercentile(samples[mode], 0.9),
					Trials: trials, IdleMillis: idle.Milliseconds(),
					Congestion: netlabSysctl("tcp_congestion_control"), SlowStart: netlabSysctl("tcp_slow_start_after_idle"),
					GzipLevel: s.wsFrameGzipLevel,
				}); err != nil {
					t.Fatalf("encode result: %v", err)
				}
			}
		}
	}
}
