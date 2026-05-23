package bulk

import (
	"os"
	"runtime"
	"strings"
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
