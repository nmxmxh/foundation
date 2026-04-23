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

func DerivePublic(cfg ServerRuntimeConfig) PublicRuntimeConfig {
	public := cfg.Public
	public.SchemaVersion = NormalizeSchemaVersion(public.SchemaVersion)
	if public.SchemaVersion == "" {
		public.SchemaVersion = NormalizeSchemaVersion(cfg.SchemaVersion)
	}
	return public
}
