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

type LocaleDefaults struct {
	Timezone string `json:"timezone"`
	Currency string `json:"currency"`
}

type PublicRuntimeConfig struct {
	SchemaVersion       string            `json:"schemaVersion,omitempty"`
	APIBaseURL          string            `json:"api_base_url"`
	WSBaseURL           string            `json:"ws_base_url"`
	AuthMode            string            `json:"auth_mode"`
	DefaultLocale       string            `json:"default_locale"`
	FeatureFlags        map[string]bool   `json:"feature_flags"`
	TransportTimeoutsMS TransportTimeouts `json:"transport_timeouts_ms"`
	WASMAssets          WASMAssets        `json:"wasm_assets"`
	DiagnosticsEnabled  bool              `json:"diagnostics_enabled"`
	LocaleDefaults      LocaleDefaults    `json:"locale_defaults"`
}

type DatabaseConfig struct {
	URL              string `json:"url"`
	MaxConnections   int    `json:"max_connections"`
	MinConnections   int    `json:"min_connections"`
	AcquireTimeoutMS int    `json:"acquire_timeout_ms"`
}

type RedisConfig struct {
	URL               string `json:"url"`
	KeyPrefix         string `json:"key_prefix"`
	DefaultTTLSeconds int    `json:"default_ttl_seconds"`
	DegradeOpen       bool   `json:"degrade_open"`
}

type ObjectStorageConfig struct {
	Endpoint        string `json:"endpoint"`
	PresignEndpoint string `json:"presign_endpoint"`
	Region          string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	UseTLS    bool   `json:"use_tls"`
	Strict    bool   `json:"strict"`
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

type CompressionConfig struct {
	APIMinBytes           int    `json:"api_min_bytes"`
	WASMPreferredEncoding string `json:"wasm_preferred_encoding"`
}

type QueueConfig struct {
	Concurrency int `json:"concurrency"`
	MaxRetries  int `json:"max_retries"`
}

type ServerRuntimeConfig struct {
	SchemaVersion string                `json:"schemaVersion,omitempty"`
	Public         PublicRuntimeConfig    `json:"public"`
	Database       DatabaseConfig         `json:"database"`
	Redis          RedisConfig            `json:"redis"`
	ObjectStorage  ObjectStorageConfig    `json:"object_storage"`
	JWT            JWTConfig              `json:"jwt"`
	RuntimeBudgets RuntimeBudgetConfig    `json:"runtime_budgets"`
	Compression    CompressionConfig      `json:"compression"`
	Queues         map[string]QueueConfig `json:"queues"`
}
