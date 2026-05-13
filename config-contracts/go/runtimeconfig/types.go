package runtimeconfig

type TransportTimeouts struct {
	HTTP int `json:"http"`
	WS   int `json:"ws"`
	WASM int `json:"wasm"`
}

type WASMAssets struct {
	ModulePath           string `json:"module_path"`
	CompressedModulePath string `json:"compressed_module_path"`
}

type RuntimeMemoryConfig struct {
	SharedMemory            string   `json:"shared_memory"`
	TransportOrder          []string `json:"transport_order"`
	Compression             []string `json:"compression"`
	ArenaBytes              int      `json:"arena_bytes,omitempty"`
	RequireSharedWASMMemory bool     `json:"require_shared_wasm_memory,omitempty"`
}

type LocaleDefaults struct {
	Timezone string `json:"timezone"`
	Currency string `json:"currency"`
}

type PublicRuntimeConfig struct {
	SchemaVersion       string              `json:"schemaVersion,omitempty"`
	APIBaseURL          string              `json:"api_base_url"`
	WSBaseURL           string              `json:"ws_base_url"`
	AuthMode            string              `json:"auth_mode"`
	DefaultLocale       string              `json:"default_locale"`
	FeatureFlags        map[string]bool     `json:"feature_flags"`
	TransportTimeoutsMS TransportTimeouts   `json:"transport_timeouts_ms"`
	WASMAssets          WASMAssets          `json:"wasm_assets"`
	RuntimeMemory       RuntimeMemoryConfig `json:"runtime_memory,omitempty"`
	DiagnosticsEnabled  bool                `json:"diagnostics_enabled"`
	LocaleDefaults      LocaleDefaults      `json:"locale_defaults"`
}

type DatabaseConfig struct {
	URL              string `json:"url"`
	MaxConnections   int    `json:"max_connections"`
	MinConnections   int    `json:"min_connections"`
	AcquireTimeoutMS int    `json:"acquire_timeout_ms"`
	QueryTimeoutMS   int    `json:"query_timeout_ms,omitempty"`
	HotReadTimeoutMS int    `json:"hot_read_timeout_ms,omitempty"`
	ShardCount       int    `json:"shard_count,omitempty"`
}

type RedisConfig struct {
	URL               string   `json:"url"`
	ShardURLs         []string `json:"shard_urls,omitempty"`
	KeyPrefix         string   `json:"key_prefix"`
	DefaultTTLSeconds int      `json:"default_ttl_seconds"`
	DegradeOpen       bool     `json:"degrade_open"`
	PoolSize          int      `json:"pool_size,omitempty"`
	MinIdle           int      `json:"min_idle,omitempty"`
	MaxRetries        int      `json:"max_retries,omitempty"`
}

type ObjectStorageConfig struct {
	Endpoint        string `json:"endpoint"`
	PresignEndpoint string `json:"presign_endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKey       string `json:"access_key"`
	SecretKey       string `json:"secret_key"`
	UseTLS          bool   `json:"use_tls"`
	Strict          bool   `json:"strict"`
}

type JWTConfig struct {
	Secret   string `json:"secret"`
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
}

type RuntimeBudgetConfig struct {
	DispatchMaxConcurrent    int `json:"dispatch_max_concurrent"`
	DispatchAcquireTimeoutMS int `json:"dispatch_acquire_timeout_ms"`
}

type SLOConfig struct {
	DispatchP99LatencyMS int     `json:"dispatch_p99_latency_ms"`
	WorkerSuccessRate    float64 `json:"worker_success_rate"`
	EventDeliveryLagMS   int     `json:"event_delivery_lag_ms"`
}

type CompressionConfig struct {
	APIMinBytes           int    `json:"api_min_bytes"`
	WASMPreferredEncoding string `json:"wasm_preferred_encoding"`
}

type QueueConfig struct {
	Concurrency int `json:"concurrency"`
	MaxRetries  int `json:"max_retries"`
}

type PostQuantumConfig struct {
	TLSHybridKEM             string `json:"tls_hybrid_kem"`
	SignatureAlgorithm       string `json:"signature_algorithm"`
	CryptoInventoryRequired  bool   `json:"crypto_inventory_required"`
	LongLivedArtifactSigning bool   `json:"long_lived_artifact_signing"`
}

type ServerSecurityConfig struct {
	RequireAuth                 bool              `json:"require_auth,omitempty"`
	ProtectOperationalEndpoints bool              `json:"protect_operational_endpoints,omitempty"`
	CORSAllowedOrigins          []string          `json:"cors_allowed_origins,omitempty"`
	PostQuantum                 PostQuantumConfig `json:"post_quantum,omitempty"`
}

type ServerRuntimeConfig struct {
	SchemaVersion  string                 `json:"schemaVersion,omitempty"`
	Public         PublicRuntimeConfig    `json:"public"`
	Database       DatabaseConfig         `json:"database"`
	Redis          RedisConfig            `json:"redis"`
	ObjectStorage  ObjectStorageConfig    `json:"object_storage"`
	JWT            JWTConfig              `json:"jwt"`
	RuntimeBudgets RuntimeBudgetConfig    `json:"runtime_budgets"`
	SLOs           SLOConfig              `json:"slos,omitempty"`
	Compression    CompressionConfig      `json:"compression"`
	Security       ServerSecurityConfig   `json:"security,omitempty"`
	Queues         map[string]QueueConfig `json:"queues"`
}
