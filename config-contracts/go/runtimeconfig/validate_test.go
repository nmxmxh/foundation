package runtimeconfig

import "testing"

func TestValidateServerRuntimeConfig(t *testing.T) {
	cfg := ServerRuntimeConfig{
		Public: PublicRuntimeConfig{
			APIBaseURL:    "https://api.example.com",
			WSBaseURL:     "wss://api.example.com/ws",
			AuthMode:      "guest-first",
			DefaultLocale: "en-NG",
			TransportTimeoutsMS: TransportTimeouts{
				HTTP: 3000,
				WS:   3000,
				WASM: 1500,
			},
			WASMAssets: WASMAssets{
				ModulePath:           "/assets/runtime.wasm",
				CompressedModulePath: "/assets/runtime.wasm.gz",
			},
			RuntimeMemory: RuntimeMemoryConfig{
				SharedMemory:            "auto",
				TransportOrder:          []string{"sab", "transferable", "postMessage", "ws", "http"},
				Compression:             []string{"br", "gzip", "deflate", "identity"},
				ArenaBytes:              8 * 1024 * 1024,
				RequireSharedWASMMemory: true,
			},
		},
		Database: DatabaseConfig{
			URL:              "postgres://example",
			MaxConnections:   12,
			MinConnections:   4,
			AcquireTimeoutMS: 3000,
		},
		Redis: RedisConfig{
			URL:               "redis://example",
			KeyPrefix:         "reframe:",
			DefaultTTLSeconds: 900,
		},
		ObjectStorage: ObjectStorageConfig{
			Endpoint:  "https://s3.example.com",
			Region:    "us-east-1",
			Bucket:    "reframe",
			AccessKey: "access",
			SecretKey: "secret",
			UseTLS:    true,
			Strict:    true,
		},
		JWT: JWTConfig{
			Secret: "secret",
			Issuer: "reframe",
		},
		RuntimeBudgets: RuntimeBudgetConfig{
			DispatchMaxConcurrent:    8,
			DispatchAcquireTimeoutMS: 1000,
		},
		SLOs: SLOConfig{
			DispatchP99LatencyMS: 100,
			WorkerSuccessRate:    0.999,
			EventDeliveryLagMS:   500,
		},
		Security: ServerSecurityConfig{
			PostQuantum: PostQuantumConfig{
				TLSHybridKEM:             "auto",
				SignatureAlgorithm:       "classical",
				CryptoInventoryRequired:  true,
				LongLivedArtifactSigning: false,
			},
		},
		Queues: map[string]QueueConfig{
			"media_probe": {Concurrency: 2, MaxRetries: 3},
		},
	}

	if err := ValidateServer(cfg); err != nil {
		t.Fatalf("ValidateServer() error = %v", err)
	}
}
