package runtimehost

type RuntimeMode string

const (
	RuntimeModeStopped    RuntimeMode = "stopped"
	RuntimeModeWorker     RuntimeMode = "worker"
	RuntimeModeMainThread RuntimeMode = "main-thread"
	RuntimeModeNative     RuntimeMode = "native"
)

type RuntimeDiagnostics struct {
	Mode              RuntimeMode `json:"mode"`
	Degraded          bool        `json:"degraded"`
	ActiveUnits       uint32      `json:"active_units"`
	InFlight          uint32      `json:"in_flight"`
	LastRuntimeSource string      `json:"last_runtime_source"`
	LastError         string      `json:"last_error"`
	LastEpoch         uint32      `json:"last_epoch"`
}

func ErrInvalidDescriptor(message string) error {
	return descriptorError(message)
}

type descriptorError string

func (e descriptorError) Error() string {
	return string(e)
}
