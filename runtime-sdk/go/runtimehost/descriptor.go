package runtimehost

type RuntimeRole string

const (
	RuntimeRolePulse   RuntimeRole = "pulse"
	RuntimeRoleCompute RuntimeRole = "compute"
	RuntimeRoleGPU     RuntimeRole = "gpu"
	RuntimeRoleIO      RuntimeRole = "io"
)

type RuntimeUnitDescriptor struct {
	UnitID               string      `json:"unit_id"`
	Role                 RuntimeRole `json:"role"`
	InputSchema          string      `json:"input_schema"`
	OutputSchema         string      `json:"output_schema"`
	SupportsWASM         bool        `json:"supports_wasm"`
	SupportsNative       bool        `json:"supports_native"`
	RequiresSharedMemory bool        `json:"requires_shared_memory"`
	SupportsGPU          bool        `json:"supports_gpu"`
	MaxConcurrency       int         `json:"max_concurrency"`
}

func (d RuntimeUnitDescriptor) Validate() error {
	if d.UnitID == "" {
		return ErrInvalidDescriptor("unit_id is required")
	}
	if d.InputSchema == "" {
		return ErrInvalidDescriptor("input_schema is required")
	}
	if d.OutputSchema == "" {
		return ErrInvalidDescriptor("output_schema is required")
	}
	if d.MaxConcurrency <= 0 {
		return ErrInvalidDescriptor("max_concurrency must be positive")
	}
	return nil
}
