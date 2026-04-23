module github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go

go 1.25.0

require (
	github.com/nmxmxh/ovasabi_foundation/server-kit/go v0.0.0
	go.uber.org/zap v1.27.1
)

require go.uber.org/multierr v1.11.0 // indirect

replace github.com/nmxmxh/ovasabi_foundation/server-kit/go => ../../server-kit/go
