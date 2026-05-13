package runtimeconfig

import (
	"sync"
	"testing"
)

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
			QueryTimeoutMS:   250,
			HotReadTimeoutMS: 50,
			ShardCount:       1,
		},
		Redis: RedisConfig{
			URL:               "redis://example",
			ShardURLs:         []string{"redis://example"},
			KeyPrefix:         "reframe:",
			DefaultTTLSeconds: 900,
			PoolSize:          32,
			MinIdle:           4,
			MaxRetries:        1,
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
			RequireAuth:                 true,
			ProtectOperationalEndpoints: true,
			CORSAllowedOrigins:          []string{"https://app.example.com"},
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

func TestValidateServerRejectsInvalidStorageBudgets(t *testing.T) {
	cfg := validServerConfigForTest()
	cfg.Database.MinConnections = cfg.Database.MaxConnections + 1
	if err := ValidateServer(cfg); err == nil {
		t.Fatal("expected invalid database pool budget to fail")
	}

	cfg = validServerConfigForTest()
	cfg.Redis.PoolSize = -1
	if err := ValidateServer(cfg); err == nil {
		t.Fatal("expected invalid redis pool budget to fail")
	}
}

func TestValidatePublicRejectsRequiredFieldsAndRuntimeMemory(t *testing.T) {
	cfg := validServerConfigForTest().Public
	cases := []struct {
		name   string
		mutate func(*PublicRuntimeConfig)
	}{
		{name: "bad schema", mutate: func(cfg *PublicRuntimeConfig) { cfg.SchemaVersion = "2.0" }},
		{name: "missing api", mutate: func(cfg *PublicRuntimeConfig) { cfg.APIBaseURL = "" }},
		{name: "missing websocket", mutate: func(cfg *PublicRuntimeConfig) { cfg.WSBaseURL = "" }},
		{name: "missing locale", mutate: func(cfg *PublicRuntimeConfig) { cfg.DefaultLocale = "" }},
		{name: "bad timeout", mutate: func(cfg *PublicRuntimeConfig) { cfg.TransportTimeoutsMS.WASM = 0 }},
		{name: "missing wasm", mutate: func(cfg *PublicRuntimeConfig) { cfg.WASMAssets.ModulePath = "" }},
		{name: "bad runtime memory", mutate: func(cfg *PublicRuntimeConfig) {
			cfg.RuntimeMemory = RuntimeMemoryConfig{
				SharedMemory:   "sometimes",
				TransportOrder: []string{"sab"},
				Compression:    []string{"identity"},
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := cfg
			tc.mutate(&next)
			if err := ValidatePublic(next); err == nil {
				t.Fatal("expected ValidatePublic to fail")
			}
		})
	}
}

func TestValidateRuntimeMemoryRejectsUnsupportedValues(t *testing.T) {
	valid := RuntimeMemoryConfig{
		SharedMemory:   "required",
		TransportOrder: []string{"postMessage", "transferable", "sab", "ws", "http"},
		Compression:    []string{"identity", "gzip", "br", "deflate"},
		ArenaBytes:     4096,
	}
	if err := ValidateRuntimeMemory(valid); err != nil {
		t.Fatalf("ValidateRuntimeMemory(valid) error = %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*RuntimeMemoryConfig)
	}{
		{name: "shared memory", mutate: func(cfg *RuntimeMemoryConfig) { cfg.SharedMemory = "bad" }},
		{name: "transport order empty", mutate: func(cfg *RuntimeMemoryConfig) { cfg.TransportOrder = nil }},
		{name: "transport unsupported", mutate: func(cfg *RuntimeMemoryConfig) { cfg.TransportOrder = []string{"pipe"} }},
		{name: "compression empty", mutate: func(cfg *RuntimeMemoryConfig) { cfg.Compression = nil }},
		{name: "compression unsupported", mutate: func(cfg *RuntimeMemoryConfig) { cfg.Compression = []string{"zstd"} }},
		{name: "negative arena", mutate: func(cfg *RuntimeMemoryConfig) { cfg.ArenaBytes = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := valid
			tc.mutate(&next)
			if err := ValidateRuntimeMemory(next); err == nil {
				t.Fatal("expected ValidateRuntimeMemory to fail")
			}
		})
	}
}

func TestValidateServerRejectsCriticalRuntimeFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ServerRuntimeConfig)
	}{
		{name: "server schema", mutate: func(cfg *ServerRuntimeConfig) { cfg.SchemaVersion = "2.0" }},
		{name: "public", mutate: func(cfg *ServerRuntimeConfig) { cfg.Public.APIBaseURL = "" }},
		{name: "database url", mutate: func(cfg *ServerRuntimeConfig) { cfg.Database.URL = "" }},
		{name: "database max", mutate: func(cfg *ServerRuntimeConfig) { cfg.Database.MaxConnections = 0 }},
		{name: "database negative budget", mutate: func(cfg *ServerRuntimeConfig) { cfg.Database.QueryTimeoutMS = -1 }},
		{name: "redis url", mutate: func(cfg *ServerRuntimeConfig) { cfg.Redis.URL = "" }},
		{name: "strict object storage", mutate: func(cfg *ServerRuntimeConfig) { cfg.ObjectStorage.SecretKey = "" }},
		{name: "jwt secret", mutate: func(cfg *ServerRuntimeConfig) { cfg.JWT.Secret = "" }},
		{name: "runtime budget", mutate: func(cfg *ServerRuntimeConfig) { cfg.RuntimeBudgets.DispatchMaxConcurrent = 0 }},
		{name: "slo", mutate: func(cfg *ServerRuntimeConfig) { cfg.SLOs.WorkerSuccessRate = 2 }},
		{name: "cors wildcard", mutate: func(cfg *ServerRuntimeConfig) { cfg.Security.CORSAllowedOrigins = []string{"*"} }},
		{name: "post quantum", mutate: func(cfg *ServerRuntimeConfig) { cfg.Security.PostQuantum.TLSHybridKEM = "unknown" }},
		{name: "queues missing", mutate: func(cfg *ServerRuntimeConfig) { cfg.Queues = nil }},
		{name: "queue concurrency", mutate: func(cfg *ServerRuntimeConfig) { cfg.Queues["media_probe"] = QueueConfig{} }},
		{name: "queue retries", mutate: func(cfg *ServerRuntimeConfig) {
			cfg.Queues["media_probe"] = QueueConfig{Concurrency: 1, MaxRetries: -1}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validServerConfigForTest()
			cfg.SLOs = SLOConfig{DispatchP99LatencyMS: 100, WorkerSuccessRate: 0.99, EventDeliveryLagMS: 50}
			cfg.Security.PostQuantum = PostQuantumConfig{TLSHybridKEM: "auto", SignatureAlgorithm: "classical"}
			tc.mutate(&cfg)
			if err := ValidateServer(cfg); err == nil {
				t.Fatal("expected ValidateServer to fail")
			}
		})
	}
}

func TestPostQuantumAndSLOValidation(t *testing.T) {
	if err := ValidatePostQuantum(PostQuantumConfig{TLSHybridKEM: "required", SignatureAlgorithm: "ml-dsa"}); err != nil {
		t.Fatalf("ValidatePostQuantum() error = %v", err)
	}
	if err := ValidatePostQuantum(PostQuantumConfig{TLSHybridKEM: "auto", SignatureAlgorithm: "slh-dsa"}); err != nil {
		t.Fatalf("ValidatePostQuantum() error = %v", err)
	}
	if err := ValidatePostQuantum(PostQuantumConfig{TLSHybridKEM: "disabled", SignatureAlgorithm: "bad"}); err == nil {
		t.Fatal("expected invalid signature algorithm to fail")
	}
	if err := ValidateSLOs(SLOConfig{DispatchP99LatencyMS: 10, WorkerSuccessRate: 1, EventDeliveryLagMS: 5}); err != nil {
		t.Fatalf("ValidateSLOs() error = %v", err)
	}
	if err := ValidateSLOs(SLOConfig{DispatchP99LatencyMS: 0, WorkerSuccessRate: 1, EventDeliveryLagMS: 5}); err == nil {
		t.Fatal("expected invalid latency SLO to fail")
	}
	if err := ValidateSLOs(SLOConfig{DispatchP99LatencyMS: 1, WorkerSuccessRate: 0, EventDeliveryLagMS: 5}); err == nil {
		t.Fatal("expected invalid worker success SLO to fail")
	}
	if err := ValidateSLOs(SLOConfig{DispatchP99LatencyMS: 1, WorkerSuccessRate: 1, EventDeliveryLagMS: 0}); err == nil {
		t.Fatal("expected invalid lag SLO to fail")
	}
}

func TestDerivePublicNormalizesSchemaVersion(t *testing.T) {
	cfg := validServerConfigForTest()
	cfg.Public.SchemaVersion = "v1"
	public := DerivePublic(cfg)
	if public.SchemaVersion != RuntimeConfigSchemaVersion {
		t.Fatalf("SchemaVersion = %q, want %q", public.SchemaVersion, RuntimeConfigSchemaVersion)
	}
}

func TestRuntimeConfigValidationConvergesAcrossConcurrentReaders(t *testing.T) {
	cfg := validServerConfigForTest()
	cfg.SchemaVersion = "v1"
	cfg.Public.SchemaVersion = "v1"
	cfg.Public.RuntimeMemory = RuntimeMemoryConfig{
		SharedMemory:   "auto",
		TransportOrder: []string{"sab", "transferable", "postMessage", "ws", "http"},
		Compression:    []string{"br", "gzip", "identity"},
		ArenaBytes:     4 * 1024 * 1024,
	}
	cfg.SLOs = SLOConfig{DispatchP99LatencyMS: 100, WorkerSuccessRate: 0.999, EventDeliveryLagMS: 250}
	cfg.Security.PostQuantum = PostQuantumConfig{TLSHybridKEM: "auto", SignatureAlgorithm: "classical"}

	const readers = 64
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ValidateServer(cfg); err != nil {
				errs <- err
				return
			}
			public := DerivePublic(cfg)
			if public.SchemaVersion != RuntimeConfigSchemaVersion {
				errs <- ValidateSchemaVersion(public.SchemaVersion)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkValidateServerRuntimeConfig(b *testing.B) {
	cfg := validServerConfigForTest()
	cfg.Public.RuntimeMemory = RuntimeMemoryConfig{
		SharedMemory:   "auto",
		TransportOrder: []string{"sab", "transferable", "postMessage", "ws", "http"},
		Compression:    []string{"br", "gzip", "identity"},
		ArenaBytes:     4 * 1024 * 1024,
	}
	cfg.SLOs = SLOConfig{DispatchP99LatencyMS: 100, WorkerSuccessRate: 0.999, EventDeliveryLagMS: 250}
	cfg.Security.PostQuantum = PostQuantumConfig{TLSHybridKEM: "auto", SignatureAlgorithm: "classical"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ValidateServer(cfg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDerivePublicRuntimeConfig(b *testing.B) {
	cfg := validServerConfigForTest()
	cfg.SchemaVersion = "v1"
	cfg.Public.SchemaVersion = "v1"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if public := DerivePublic(cfg); public.SchemaVersion != RuntimeConfigSchemaVersion {
			b.Fatalf("schema = %q, want %q", public.SchemaVersion, RuntimeConfigSchemaVersion)
		}
	}
}

func validServerConfigForTest() ServerRuntimeConfig {
	return ServerRuntimeConfig{
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
			WASMAssets: WASMAssets{ModulePath: "/assets/runtime.wasm"},
		},
		Database: DatabaseConfig{
			URL:              "postgres://example",
			MaxConnections:   12,
			MinConnections:   4,
			AcquireTimeoutMS: 3000,
			QueryTimeoutMS:   250,
			HotReadTimeoutMS: 50,
			ShardCount:       1,
		},
		Redis: RedisConfig{
			URL:               "redis://example",
			KeyPrefix:         "reframe:",
			DefaultTTLSeconds: 900,
			PoolSize:          32,
			MinIdle:           4,
			MaxRetries:        1,
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
		Queues: map[string]QueueConfig{
			"media_probe": {Concurrency: 2, MaxRetries: 3},
		},
	}
}
