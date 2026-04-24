package runtimeconfig

import (
	"fmt"
	"strings"
)

const RuntimeConfigSchemaVersion = "1.0"

var runtimeConfigSchemaAliases = map[string]string{
	"v1": RuntimeConfigSchemaVersion,
}

func NormalizeSchemaVersion(version string) string {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return RuntimeConfigSchemaVersion
	}
	if alias, ok := runtimeConfigSchemaAliases[trimmed]; ok {
		return alias
	}
	return trimmed
}

func ValidateSchemaVersion(version string) error {
	if NormalizeSchemaVersion(version) != RuntimeConfigSchemaVersion {
		return fmt.Errorf("unsupported runtime config schema version %q", strings.TrimSpace(version))
	}
	return nil
}

func ValidatePublic(cfg PublicRuntimeConfig) error {
	if err := ValidateSchemaVersion(cfg.SchemaVersion); err != nil {
		return err
	}
	if cfg.APIBaseURL == "" {
		return fmt.Errorf("api_base_url is required")
	}
	if cfg.WSBaseURL == "" {
		return fmt.Errorf("ws_base_url is required")
	}
	if cfg.DefaultLocale == "" {
		return fmt.Errorf("default_locale is required")
	}
	if cfg.TransportTimeoutsMS.HTTP <= 0 || cfg.TransportTimeoutsMS.WS <= 0 || cfg.TransportTimeoutsMS.WASM <= 0 {
		return fmt.Errorf("transport_timeout_ms values must be positive")
	}
	if cfg.WASMAssets.ModulePath == "" {
		return fmt.Errorf("wasm_assets.module_path is required")
	}
	if !runtimeMemoryConfigIsZero(cfg.RuntimeMemory) {
		if err := ValidateRuntimeMemory(cfg.RuntimeMemory); err != nil {
			return err
		}
	}
	return nil
}

func ValidateRuntimeMemory(cfg RuntimeMemoryConfig) error {
	if !oneOf(cfg.SharedMemory, "off", "auto", "required") {
		return fmt.Errorf("runtime_memory.shared_memory must be off, auto, or required")
	}
	if len(cfg.TransportOrder) == 0 {
		return fmt.Errorf("runtime_memory.transport_order requires at least one transport")
	}
	for _, transport := range cfg.TransportOrder {
		if !oneOf(transport, "postMessage", "transferable", "sab", "ws", "http") {
			return fmt.Errorf("runtime_memory.transport_order contains unsupported transport %q", transport)
		}
	}
	if len(cfg.Compression) == 0 {
		return fmt.Errorf("runtime_memory.compression requires at least one encoding")
	}
	for _, encoding := range cfg.Compression {
		if !oneOf(encoding, "identity", "gzip", "br", "deflate") {
			return fmt.Errorf("runtime_memory.compression contains unsupported encoding %q", encoding)
		}
	}
	if cfg.ArenaBytes < 0 {
		return fmt.Errorf("runtime_memory.arena_bytes must be positive when set")
	}
	return nil
}

func ValidateServer(cfg ServerRuntimeConfig) error {
	if err := ValidateSchemaVersion(cfg.SchemaVersion); err != nil {
		return err
	}
	if err := ValidatePublic(cfg.Public); err != nil {
		return err
	}
	if cfg.Database.URL == "" {
		return fmt.Errorf("database.url is required")
	}
	if cfg.Database.MaxConnections <= 0 || cfg.Database.AcquireTimeoutMS <= 0 {
		return fmt.Errorf("database runtime budgets must be positive")
	}
	if cfg.Redis.URL == "" || cfg.Redis.KeyPrefix == "" || cfg.Redis.DefaultTTLSeconds <= 0 {
		return fmt.Errorf("redis url, key_prefix, and default_ttl_seconds are required")
	}
	if cfg.ObjectStorage.Strict && (cfg.ObjectStorage.Endpoint == "" || cfg.ObjectStorage.Region == "" || cfg.ObjectStorage.Bucket == "" || cfg.ObjectStorage.AccessKey == "" || cfg.ObjectStorage.SecretKey == "") {
		return fmt.Errorf("strict object storage requires endpoint, region, bucket, access key, and secret key")
	}
	if cfg.JWT.Secret == "" || cfg.JWT.Issuer == "" {
		return fmt.Errorf("jwt secret and issuer are required")
	}
	if cfg.RuntimeBudgets.DispatchMaxConcurrent <= 0 || cfg.RuntimeBudgets.DispatchAcquireTimeoutMS <= 0 {
		return fmt.Errorf("runtime budgets must be positive")
	}
	if !sloConfigIsZero(cfg.SLOs) {
		if err := ValidateSLOs(cfg.SLOs); err != nil {
			return err
		}
	}
	if !postQuantumConfigIsZero(cfg.Security.PostQuantum) {
		if err := ValidatePostQuantum(cfg.Security.PostQuantum); err != nil {
			return err
		}
	}
	if len(cfg.Queues) == 0 {
		return fmt.Errorf("at least one queue configuration is required")
	}
	for name, queue := range cfg.Queues {
		if queue.Concurrency <= 0 {
			return fmt.Errorf("queue %s concurrency must be positive", name)
		}
		if queue.MaxRetries < 0 {
			return fmt.Errorf("queue %s max_retries must be zero or greater", name)
		}
	}
	return nil
}

func ValidatePostQuantum(cfg PostQuantumConfig) error {
	if !oneOf(cfg.TLSHybridKEM, "auto", "required", "disabled") {
		return fmt.Errorf("security.post_quantum.tls_hybrid_kem must be auto, required, or disabled")
	}
	if !oneOf(cfg.SignatureAlgorithm, "classical", "ml-dsa", "slh-dsa") {
		return fmt.Errorf("security.post_quantum.signature_algorithm must be classical, ml-dsa, or slh-dsa")
	}
	return nil
}

func ValidateSLOs(cfg SLOConfig) error {
	if cfg.DispatchP99LatencyMS <= 0 {
		return fmt.Errorf("slos.dispatch_p99_latency_ms must be positive")
	}
	if cfg.WorkerSuccessRate <= 0 || cfg.WorkerSuccessRate > 1 {
		return fmt.Errorf("slos.worker_success_rate must be between 0 and 1")
	}
	if cfg.EventDeliveryLagMS <= 0 {
		return fmt.Errorf("slos.event_delivery_lag_ms must be positive")
	}
	return nil
}

func DerivePublic(cfg ServerRuntimeConfig) PublicRuntimeConfig {
	public := cfg.Public
	public.SchemaVersion = NormalizeSchemaVersion(public.SchemaVersion)
	if public.SchemaVersion == "" {
		public.SchemaVersion = NormalizeSchemaVersion(cfg.SchemaVersion)
	}
	return public
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

func runtimeMemoryConfigIsZero(cfg RuntimeMemoryConfig) bool {
	return cfg.SharedMemory == "" && len(cfg.TransportOrder) == 0 && len(cfg.Compression) == 0 && cfg.ArenaBytes == 0 && !cfg.RequireSharedWASMMemory
}

func postQuantumConfigIsZero(cfg PostQuantumConfig) bool {
	return cfg.TLSHybridKEM == "" && cfg.SignatureAlgorithm == "" && !cfg.CryptoInventoryRequired && !cfg.LongLivedArtifactSigning
}

func sloConfigIsZero(cfg SLOConfig) bool {
	return cfg.DispatchP99LatencyMS == 0 && cfg.WorkerSuccessRate == 0 && cfg.EventDeliveryLagMS == 0
}
