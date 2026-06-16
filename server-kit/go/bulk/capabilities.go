package bulk

import (
	"context"
	"os"
	"runtime"
	"strings"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/kernellane"
)

type PlatformCapabilities struct {
	OS                string            `json:"os"`
	ZeroCopyAvailable bool              `json:"zero_copy_available"`
	MPTCPAvailable    bool              `json:"mptcp_available"`
	QUICAvailable     bool              `json:"quic_available"`
	KernelPacing      bool              `json:"kernel_pacing"`
	Notes             map[string]string `json:"notes,omitempty"`
}

func DetectPlatformCapabilities() PlatformCapabilities {
	return detectPlatformCapabilities(runtime.GOOS, linuxMPTCPEnabled)
}

func detectPlatformCapabilities(goos string, mptcpEnabled func() bool) PlatformCapabilities {
	caps := PlatformCapabilities{
		OS:    goos,
		Notes: map[string]string{},
	}
	if goos != "linux" {
		caps.Notes["fallback"] = "kernel bulk accelerators require a Linux adapter"
		return caps
	}
	caps.ZeroCopyAvailable = true
	caps.KernelPacing = true
	caps.MPTCPAvailable = mptcpEnabled()
	caps.Notes["zero_copy"] = "candidate only; adapter must manage pinned-buffer lifetime and completions"
	caps.Notes["kernel_pacing"] = "candidate only; adapter must set socket pacing policy"
	if caps.MPTCPAvailable {
		caps.Notes["mptcp"] = "kernel MPTCP appears enabled"
	} else {
		caps.Notes["mptcp"] = "kernel MPTCP not detected or disabled"
	}
	caps.Notes["quic"] = "requires an explicit QUIC/HTTP3 adapter"
	return caps
}

// DetectPlatformCapabilitiesProbed augments the static OS detection with real
// runtime probes from the kernellane package: it confirms kernel file zero-copy
// via a copy_file_range probe and MPTCP via an actual loopback negotiation,
// rather than inferring them from the OS name. The probes are cached, so call it
// once at startup and pass the result into LaneRequest.Capabilities — this feeds
// the lane planner genuine capability instead of optimistic "linux implies
// available" guesses. Any unsupported accelerator degrades to its portable lane.
func DetectPlatformCapabilitiesProbed(ctx context.Context) PlatformCapabilities {
	caps := DetectPlatformCapabilities() // always returns an initialized Notes map
	caps.ZeroCopyAvailable = kernellane.ZeroCopyFileSupported()
	caps.MPTCPAvailable = kernellane.MultipathTCPSupported(ctx)
	caps.Notes["zero_copy"] = zeroCopyNote(caps.ZeroCopyAvailable)
	caps.Notes["mptcp"] = mptcpNote(caps.MPTCPAvailable)
	return caps
}

func zeroCopyNote(available bool) string {
	if available {
		return "verified: copy_file_range probe succeeded"
	}
	return "kernel copy_file_range unavailable; portable copy fallback"
}

func mptcpNote(available bool) string {
	if available {
		return "verified: MPTCP negotiated on loopback probe"
	}
	return "MPTCP not negotiated; ordinary TCP fallback"
}

func (c PlatformCapabilities) PipelineOptions() PipelineOptions {
	return PipelineOptions{
		ZeroCopyAvailable: c.ZeroCopyAvailable,
		MPTCPAvailable:    c.MPTCPAvailable,
		QUICAvailable:     c.QUICAvailable,
		KernelPacing:      c.KernelPacing,
		Attributes:        cloneStringMap(c.Notes),
	}
}

func linuxMPTCPEnabled() bool {
	return linuxMPTCPEnabledWith(os.ReadFile)
}

func linuxMPTCPEnabledWith(readFile func(string) ([]byte, error)) bool {
	payload, err := readFile("/proc/sys/net/mptcp/enabled")
	if err != nil {
		return false
	}
	value := strings.TrimSpace(string(payload))
	return value == "1" || strings.EqualFold(value, "y") || strings.EqualFold(value, "enabled")
}
