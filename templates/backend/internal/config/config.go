// Package config loads and validates application runtime configuration.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration
type Config struct {
	// Application
	Env      string
	Port     int
	LogLevel string

	// Ingress security
	AllowedOrigins              []string
	RequireAuth                 bool
	ProtectOperationalEndpoints bool

	// Database
	DatabaseURL         string
	StateStore          string
	DBHost              string
	DBPort              int
	DBUser              string
	DBPassword          string
	DBName              string
	DBSSLMode           string
	DBMaxConns          int
	DBMinConns          int
	DBHealthCheckPeriod time.Duration
	DBConnectTimeout    time.Duration
	DBAcquireTimeout    time.Duration
	DBQueryTimeout      time.Duration
	DBHotReadTimeout    time.Duration
	DBShardCount        int
	HermesMaxRecords    int
	HermesMaxBytes      int64
	HermesIndexedFields []string
	// HermesWarmScopes lists projection scopes to eagerly warm from the database
	// at startup so the projection gateway serves out-of-band (e.g. SQL-seeded)
	// rows instead of "projection not found". Each entry is
	// "domain:collection:organization"; empty organization is invalid.
	HermesWarmScopes []string
	// HermesSnapshotDir enables the shadow-mode snapshot rollout: after every
	// source rebuild the projected store diffs and refreshes a durable snapshot
	// artifact under this directory (evidence counters in hermes runtime
	// stats). Empty disables the shadow lane. The served warm path never
	// changes: snapshots are compared and produced, not yet preferred.
	HermesSnapshotDir string
	// HermesEnvelopeFallback runs a hardened EnvelopeTailer per warm scope,
	// consuming canonical projection envelopes from Redis Streams
	// (hermes:projection:<domain>:<collection>:<organization>). It is the
	// fallback population path for producers that cannot share the Postgres
	// job queue the canonical RecordWorkerProcessor uses.
	HermesEnvelopeFallback bool

	// Redis
	RedisURL          string
	RedisShardURLs    string
	RedisPrefix       string
	RedisPoolSize     int
	RedisMinIdle      int
	RedisMaxRetries   int
	RedisDialTimeout  time.Duration
	RedisReadTimeout  time.Duration
	RedisWriteTimeout time.Duration
	EventBus          string

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
	env := getEnv("APP_ENV", "development")
	cfg := &Config{
		Env:                                 env,
		Port:                                getEnvInt("PORT", 8080),
		LogLevel:                            getEnv("LOG_LEVEL", "info"),
		AllowedOrigins:                      splitCSV(getEnv("ALLOWED_ORIGINS", defaultAllowedOrigins(env))),
		RequireAuth:                         getEnvBool("REQUIRE_AUTH", env == "production"),
		ProtectOperationalEndpoints:         getEnvBool("PROTECT_OPERATIONAL_ENDPOINTS", env == "production"),
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
		DBHealthCheckPeriod:                 getEnvDuration("DB_HEALTHCHECK_PERIOD", time.Duration(getEnvInt("DB_HEALTHCHECK_PERIOD_SECONDS", 30))*time.Second),
		DBConnectTimeout:                    getEnvDuration("DB_CONNECT_TIMEOUT", time.Duration(getEnvInt("DB_CONNECT_TIMEOUT_SECONDS", 10))*time.Second),
		DBAcquireTimeout:                    getEnvDuration("DB_ACQUIRE_TIMEOUT", 100*time.Millisecond),
		DBQueryTimeout:                      getEnvDuration("DB_QUERY_TIMEOUT", 250*time.Millisecond),
		DBHotReadTimeout:                    getEnvDuration("DB_HOT_READ_TIMEOUT", 50*time.Millisecond),
		DBShardCount:                        getEnvInt("DB_SHARD_COUNT", 1),
		HermesMaxRecords:                    getEnvInt("HERMES_MAX_RECORDS_PER_SCOPE", 10000),
		HermesMaxBytes:                      int64(getEnvInt("HERMES_MAX_BYTES_PER_SCOPE", 16*1024*1024)),
		HermesIndexedFields:                 splitCSV(getEnv("HERMES_INDEXED_FIELDS", "state,status,type,kind,bucket")),
		HermesWarmScopes:                    splitCSV(getEnv("HERMES_WARM_SCOPES", "")),
		HermesSnapshotDir:                   getEnv("HERMES_SNAPSHOT_DIR", ""),
		HermesEnvelopeFallback:              getEnvBool("HERMES_ENVELOPE_FALLBACK", false),
		RedisURL:                            getEnv("REDIS_URL", ""),
		RedisShardURLs:                      getEnv("REDIS_SHARD_URLS", ""),
		RedisPrefix:                         getEnv("REDIS_PREFIX", "{{PROJECT_NAME}}"),
		RedisPoolSize:                       getEnvInt("REDIS_POOL_SIZE", 32),
		RedisMinIdle:                        getEnvInt("REDIS_MIN_IDLE", 4),
		RedisMaxRetries:                     getEnvInt("REDIS_MAX_RETRIES", 1),
		RedisDialTimeout:                    getEnvDuration("REDIS_DIAL_TIMEOUT", 2*time.Second),
		RedisReadTimeout:                    getEnvDuration("REDIS_READ_TIMEOUT", 500*time.Millisecond),
		RedisWriteTimeout:                   getEnvDuration("REDIS_WRITE_TIMEOUT", 500*time.Millisecond),
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
	if c.RequireAuth && c.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is required when REQUIRE_AUTH is true")
	}
	if c.IsProduction() && len(c.AllowedOrigins) == 0 {
		return fmt.Errorf("ALLOWED_ORIGINS must contain explicit origins in production")
	}
	for _, origin := range c.AllowedOrigins {
		if origin == "*" && c.IsProduction() {
			return fmt.Errorf("ALLOWED_ORIGINS cannot contain wildcard origins in production")
		}
	}
	if c.DBMaxConns < 0 || c.DBMinConns < 0 || c.DBShardCount < 0 {
		return fmt.Errorf("database pool and shard settings must be zero or greater")
	}
	if c.DBMaxConns > 0 && c.DBMinConns > c.DBMaxConns {
		return fmt.Errorf("DB_MIN_CONNS cannot exceed DB_MAX_CONNS")
	}
	if c.DBHealthCheckPeriod <= 0 || c.DBConnectTimeout <= 0 || c.DBAcquireTimeout <= 0 || c.DBQueryTimeout <= 0 || c.DBHotReadTimeout <= 0 {
		return fmt.Errorf("database timeout settings must be positive")
	}
	if c.HermesMaxRecords <= 0 || c.HermesMaxBytes <= 0 || len(c.HermesIndexedFields) == 0 {
		return fmt.Errorf("hermes bounds and indexed fields must be configured")
	}
	if c.RedisPoolSize < 0 || c.RedisMinIdle < 0 || c.RedisMaxRetries < 0 {
		return fmt.Errorf("redis pool settings must be zero or greater")
	}
	if c.RedisDialTimeout <= 0 || c.RedisReadTimeout <= 0 || c.RedisWriteTimeout <= 0 {
		return fmt.Errorf("redis timeout settings must be positive")
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

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func defaultAllowedOrigins(env string) string {
	if env == "production" {
		return ""
	}
	return "http://localhost:3000,http://localhost:5173,http://localhost:8080"
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
