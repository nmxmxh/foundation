module github.com/nmxmxh/ovasabi_foundation/runtime-sdk/go

go 1.26.0

require github.com/nmxmxh/ovasabi_foundation/server-kit/go v0.0.0

require (
	github.com/nmxmxh/ovasabi_foundation/runtime-transport/go v0.0.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/nmxmxh/ovasabi_foundation/server-kit/go => ../../server-kit/go

replace github.com/nmxmxh/ovasabi_foundation/runtime-transport/go => ../../runtime-transport/go
