// Package config loads and validates application runtime configuration.
package config

import (
	"fmt"
	"net"
	"net/url"
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
	DatabaseURL      string
	StateStore       string
	DBHost           string
	DBPort           int
	DBUser           string
	DBPassword       string
	DBName           string
	DBSSLMode        string
	DBMaxConns       int
	DBMinConns       int
	DBAcquireTimeout time.Duration
	DBQueryTimeout   time.Duration
	DBHotReadTimeout time.Duration
	DBShardCount     int

	// Redis
	RedisURL        string
	RedisShardURLs  string
	RedisPrefix     string
	RedisPoolSize   int
	RedisMinIdle    int
	RedisMaxRetries int
	EventBus        string

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
		DBHost:                              getEnv("DB_HOST", ""),
		DBPort:                              getEnvInt("DB_PORT", 5432),
		DBUser:                              getEnv("DB_USER", "postgres"),
		DBPassword:                          getEnv("DB_PASSWORD", ""),
		DBName:                              getEnv("DB_NAME", "{{PROJECT_NAME}}_dev"),
		DBSSLMode:                           getEnv("DB_SSLMODE", "disable"),
		DBMaxConns:                          getEnvInt("DB_MAX_CONNS", 0),
		DBMinConns:                          getEnvInt("DB_MIN_CONNS", 0),
		DBAcquireTimeout:                    getEnvDuration("DB_ACQUIRE_TIMEOUT", 100*time.Millisecond),
		DBQueryTimeout:                      getEnvDuration("DB_QUERY_TIMEOUT", 250*time.Millisecond),
		DBHotReadTimeout:                    getEnvDuration("DB_HOT_READ_TIMEOUT", 50*time.Millisecond),
		DBShardCount:                        getEnvInt("DB_SHARD_COUNT", 1),
		RedisURL:                            getEnv("REDIS_URL", ""),
		RedisShardURLs:                      getEnv("REDIS_SHARD_URLS", ""),
		RedisPrefix:                         getEnv("REDIS_PREFIX", "{{PROJECT_NAME}}"),
		RedisPoolSize:                       getEnvInt("REDIS_POOL_SIZE", 32),
		RedisMinIdle:                        getEnvInt("REDIS_MIN_IDLE", 4),
		RedisMaxRetries:                     getEnvInt("REDIS_MAX_RETRIES", 1),
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
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = databaseURLFromParts(cfg.DBUser, cfg.DBPassword, cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBSSLMode)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func databaseURLFromParts(user, password, host string, port int, dbName, sslMode string) string {
	if host == "" || dbName == "" {
		return ""
	}
	if port <= 0 {
		port = 5432
	}
	if user == "" {
		user = "postgres"
	}
	if sslMode == "" {
		sslMode = "disable"
	}
	userInfo := url.User(user)
	if password != "" {
		userInfo = url.UserPassword(user, password)
	}
	u := url.URL{
		Scheme: "postgres",
		User:   userInfo,
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/" + dbName,
	}
	q := u.Query()
	q.Set("sslmode", sslMode)
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Config) validate() error {
	if c.DatabaseURL == "" && c.StateStore != "memory" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if c.JWTSecret == "" && c.Env == "production" {
		return fmt.Errorf("JWT_SECRET is required in production")
	}
	if c.DBMaxConns < 0 || c.DBMinConns < 0 || c.DBShardCount < 0 {
		return fmt.Errorf("database pool and shard settings must be zero or greater")
	}
	if c.DBMaxConns > 0 && c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("DB_MIN_CONNS cannot exceed DB_MAX_CONNS")
	}
	if c.DBAcquireTimeout <= 0 || c.DBQueryTimeout <= 0 || c.DBHotReadTimeout <= 0 {
		return fmt.Errorf("database timeout settings must be positive")
	}
	if c.RedisPoolSize < 0 || c.RedisMinIdle < 0 || c.RedisMaxRetries < 0 {
		return fmt.Errorf("redis pool settings must be zero or greater")
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
