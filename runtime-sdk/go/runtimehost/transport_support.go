package runtimehost

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrProcessTransportUnsupported      = errors.New("process transport is not supported on this runtime")
	ErrSharedMemoryTransportUnsupported = errors.New("shared memory transport is not supported on this runtime")
	ErrFFITransportUnsupported          = errors.New("ffi runtime transport is not supported on this runtime")
)

const DefaultProcessExchangeTimeout = 30 * time.Second

type ProcessTransportSupport struct {
	Requested ProcessTransportMode   `json:"requested"`
	Resolved  ProcessTransportMode   `json:"resolved"`
	Supported []ProcessTransportMode `json:"supported"`
	Fallback  bool                   `json:"fallback"`
	Reason    string                 `json:"reason,omitempty"`
}

type ProcessWorkerSnapshot struct {
	Index        int                  `json:"index"`
	Mode         ProcessTransportMode `json:"mode"`
	Busy         bool                 `json:"busy"`
	RestartCount uint32               `json:"restart_count"`
	LastError    string               `json:"last_error,omitempty"`
	LastStarted  time.Time            `json:"last_started,omitempty"`
	LastSuccess  time.Time            `json:"last_success,omitempty"`
	LastFailure  time.Time            `json:"last_failure,omitempty"`
}

type ProcessPoolDiagnostics struct {
	Transport         ProcessTransportSupport `json:"transport"`
	ExchangeTimeoutMS int64                   `json:"exchange_timeout_ms"`
	Workers           []ProcessWorkerSnapshot `json:"workers"`
}

func SupportedProcessTransports(sharedMemoryDir string) []ProcessTransportMode {
	supported := []ProcessTransportMode{ProcessTransportStdio}
	if ffiSupported() {
		supported = append(supported, ProcessTransportFFI)
	}
	if sharedMemorySupported(sharedMemoryDir) {
		supported = append(supported, ProcessTransportSharedMemory)
	}
	return supported
}

func ResolveProcessTransportSupport(requested ProcessTransportMode, sharedMemoryDir string) (ProcessTransportSupport, error) {
	mode := normalizeProcessTransportMode(requested)
	support := ProcessTransportSupport{
		Requested: mode,
		Supported: SupportedProcessTransports(sharedMemoryDir),
	}

	switch mode {
	case ProcessTransportAuto:
		if sharedMemorySupported(sharedMemoryDir) {
			support.Resolved = ProcessTransportSharedMemory
			return support, nil
		}
		support.Resolved = ProcessTransportStdio
		support.Fallback = true
		support.Reason = "shared memory transport is unavailable on this runtime; using stdio"
		return support, nil
	case ProcessTransportFFI:
		if !ffiSupported() {
			return support, fmt.Errorf("%w: cgo-enabled linux or darwin build required", ErrFFITransportUnsupported)
		}
		support.Resolved = ProcessTransportFFI
		return support, nil
	case ProcessTransportStdio:
		support.Resolved = ProcessTransportStdio
		return support, nil
	case ProcessTransportSharedMemory:
		if !sharedMemorySupported(sharedMemoryDir) {
			return support, fmt.Errorf("%w: linux runtime required", ErrSharedMemoryTransportUnsupported)
		}
		support.Resolved = ProcessTransportSharedMemory
		return support, nil
	default:
		return support, fmt.Errorf("%w %q", ErrProcessTransportUnsupported, strings.TrimSpace(string(requested)))
	}
}
