module github.com/nmxmxh/ovasabi_foundation/server-kit/go

go 1.26.10

require (
	github.com/SherClockHolmes/webpush-go v1.4.0
	github.com/andybalholm/brotli v1.2.1
	github.com/aws/aws-sdk-go v1.55.7
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/jackc/pgx/v5 v5.10.0
	github.com/klauspost/compress v1.18.7
	github.com/nmxmxh/ovasabi_foundation/config-contracts/go v0.0.0
	github.com/nmxmxh/ovasabi_foundation/runtime-transport/go v0.0.0
	github.com/redis/go-redis/v9 v9.17.2
	github.com/riverqueue/river v0.44.1
	github.com/riverqueue/river/riverdriver/riverpgxv5 v0.44.1
	github.com/stretchr/testify v1.11.1
	go.opentelemetry.io/otel v1.45.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.45.0
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.45.0
	go.opentelemetry.io/otel/sdk v1.45.0
	go.opentelemetry.io/otel/trace v1.45.0
	golang.org/x/net v0.60.0
	golang.org/x/sync v0.23.0
	golang.org/x/sys v0.48.0
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/riverqueue/river/riverdriver v0.44.1 // indirect
	github.com/riverqueue/river/rivershared v0.44.1 // indirect
	github.com/riverqueue/river/rivertype v0.44.1 // indirect
	github.com/tidwall/gjson v1.19.0
	github.com/tidwall/match v1.2.0 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.45.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260803160001-6ac0973c030d // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/nmxmxh/ovasabi_foundation/config-contracts/go => ../../config-contracts/go

replace github.com/nmxmxh/ovasabi_foundation/runtime-transport/go => ../../runtime-transport/go
