package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all application configuration
type Config struct {
	// Application
	Env      string
	Port     int
	LogLevel string

	// Database
	DatabaseURL string
	StateStore  string

	// Redis
	RedisURL    string
	RedisPrefix string
	EventBus    string

	// JWT
	JWTSecret     string
	JWTExpiration time.Duration

	// Runtime
	RuntimeSharedMemory            string
	RuntimeTransportOrder          string
	RuntimeCompression             string
	RuntimeArenaBytes              int
	RuntimeRequireSharedWASMMemory bool

	// Post-quantum posture
	PostQuantumTLSHybridKEM             string
	PostQuantumSignatureAlgorithm       string
	PostQuantumCryptoInventoryRequired  bool
	PostQuantumLongLivedArtifactSigning bool

	// External services
	// Add your external service configs here
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		Env:                                 getEnv("APP_ENV", "development"),
		Port:                                getEnvInt("PORT", 8080),
		LogLevel:                            getEnv("LOG_LEVEL", "info"),
		DatabaseURL:                         getEnv("DATABASE_URL", ""),
		StateStore:                          getEnv("STATE_STORE_DRIVER", "postgres"),
		RedisURL:                            getEnv("REDIS_URL", ""),
		RedisPrefix:                         getEnv("REDIS_PREFIX", "{{PROJECT_NAME}}"),
		EventBus:                            getEnv("EVENT_BUS_DRIVER", "redis"),
		JWTSecret:                           getEnv("JWT_SECRET", ""),
		JWTExpiration:                       getEnvDuration("JWT_EXPIRATION", 24*time.Hour),
		RuntimeSharedMemory:                 getEnv("RUNTIME_SHARED_MEMORY", "auto"),
		RuntimeTransportOrder:               getEnv("RUNTIME_TRANSPORT_ORDER", "sab,transferable,postMessage,ws,http"),
		RuntimeCompression:                  getEnv("RUNTIME_COMPRESSION", "br,gzip,deflate,identity"),
		RuntimeArenaBytes:                   getEnvInt("RUNTIME_ARENA_BYTES", 8*1024*1024),
		RuntimeRequireSharedWASMMemory:      getEnvBool("RUNTIME_REQUIRE_SHARED_WASM_MEMORY", false),
		PostQuantumTLSHybridKEM:             getEnv("POST_QUANTUM_TLS_HYBRID_KEM", "auto"),
		PostQuantumSignatureAlgorithm:       getEnv("POST_QUANTUM_SIGNATURE_ALGORITHM", "classical"),
		PostQuantumCryptoInventoryRequired:  getEnvBool("POST_QUANTUM_CRYPTO_INVENTORY_REQUIRED", true),
		PostQuantumLongLivedArtifactSigning: getEnvBool("POST_QUANTUM_LONG_LIVED_ARTIFACT_SIGNING", false),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.JWTSecret == "" && c.Env == "production" {
		return fmt.Errorf("JWT_SECRET is required in production")
	}
	if !oneOf(c.RuntimeSharedMemory, "off", "auto", "required") {
		return fmt.Errorf("RUNTIME_SHARED_MEMORY must be off, auto, or required")
	}
	if !oneOf(c.PostQuantumTLSHybridKEM, "auto", "required", "disabled") {
		return fmt.Errorf("POST_QUANTUM_TLS_HYBRID_KEM must be auto, required, or disabled")
	}
	if !oneOf(c.PostQuantumSignatureAlgorithm, "classical", "ml-dsa", "slh-dsa") {
		return fmt.Errorf("POST_QUANTUM_SIGNATURE_ALGORITHM must be classical, ml-dsa, or slh-dsa")
	}
	return nil
}

func (c *Config) IsDevelopment() bool {
	return c.Env == "development"
}

func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

func (c *Config) IsTest() bool {
	return c.Env == "test"
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return fallback
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
