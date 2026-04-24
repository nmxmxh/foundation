package testutil

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgxmock "github.com/pashagolub/pgxmock/v4"
	"github.com/redis/go-redis/v9"
)

const (
	defaultTestDatabaseName = "{{PROJECT_NAME}}_test"
	defaultTestRedisPrefix  = "test_{{PROJECT_NAME}}:"
)

// TestEnv provides a mock-based test environment for unit tests.
// Use this when you want fast, isolated tests without real infrastructure.
type TestEnv struct {
	DB      pgxmock.PgxPoolIface
	cleanup []func()
}

// RealTestEnv provides a real database test environment for integration tests.
// Use this when you need to test actual database interactions.
type RealTestEnv struct {
	DB             *pgxpool.Pool
	Redis          *redis.Client
	cleanup        []func()
	CreatedUserIDs []string
	CreatedOrgIDs  []string
}

type testDBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
}

type testRedisConfig struct {
	Host     string
	Port     string
	Password string
	DB       string
}

// SetupTestEnv creates a mock-based test environment for unit tests.
// The returned TestEnv provides a pgxmock interface for mocking database calls.
func SetupTestEnv(t *testing.T) *TestEnv {
	mock, err := pgxmock.NewPool(pgxmock.QueryMatcherOption(pgxmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("failed to create pgxmock pool: %v", err)
	}

	env := &TestEnv{
		DB: mock,
	}

	t.Cleanup(func() {
		env.Teardown()
	})

	return env
}

// Teardown cleans up all resources in reverse order.
func (e *TestEnv) Teardown() {
	for i := len(e.cleanup) - 1; i >= 0; i-- {
		e.cleanup[i]()
	}
	if e.DB != nil {
		e.DB.Close()
	}
}

// ExpectationsWereMet verifies all mock expectations were satisfied.
func (e *TestEnv) ExpectationsWereMet(t *testing.T) {
	if err := e.DB.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet mock expectations: %v", err)
	}
}

// SetupRealTestEnv creates a test environment with real database connections.
// Use make test-integration when infrastructure should be required rather than skipped.
func SetupRealTestEnv(t *testing.T) *RealTestEnv {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbURL, redisURL := ApplyTestEnvDefaults()

	dbConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		skipOrFailInfra(t, "unable to parse database URL: %v", err)
	}

	dbConfig.MaxConns = int32(intFromEnvOrDefault("TEST_DB_MAX_CONNS", 10))
	dbConfig.MinConns = 1
	dbConfig.MaxConnLifetime = 5 * time.Minute
	dbConfig.MaxConnIdleTime = 1 * time.Minute
	dbConfig.HealthCheckPeriod = 30 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbPool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		skipOrFailInfra(t, "unable to connect to database: %v", err)
	}

	if err := dbPool.Ping(ctx); err != nil {
		dbPool.Close()
		skipOrFailInfra(t, "unable to ping database: %v", err)
	}

	var redisClient *redis.Client
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		if boolFromEnv("TEST_REDIS_REQUIRED") || boolFromEnv("TEST_INFRA_REQUIRED") {
			t.Fatalf("unable to parse Redis URL: %v", err)
		}
		t.Logf("redis URL unavailable, continuing without it: %v", err)
	} else {
		redisClient = redis.NewClient(opts)
		if err := redisClient.Ping(ctx).Err(); err != nil {
			_ = redisClient.Close()
			redisClient = nil
			if boolFromEnv("TEST_REDIS_REQUIRED") || boolFromEnv("TEST_INFRA_REQUIRED") {
				t.Fatalf("unable to ping Redis: %v", err)
			}
			t.Logf("redis unavailable, continuing without it: %v", err)
		}
	}

	env := &RealTestEnv{
		DB:             dbPool,
		Redis:          redisClient,
		CreatedUserIDs: []string{},
		CreatedOrgIDs:  []string{},
	}

	t.Cleanup(func() {
		env.Teardown(t)
	})

	return env
}

// Teardown cleans up test data and closes connections.
func (e *RealTestEnv) Teardown(t *testing.T) {
	ctx := context.Background()

	for i := len(e.cleanup) - 1; i >= 0; i-- {
		e.cleanup[i]()
	}

	e.cleanupTrackedEntities(ctx, t)

	if e.Redis != nil {
		_ = e.Redis.Close()
	}
	if e.DB != nil {
		e.DB.Close()
	}
}

// cleanupTrackedEntities deletes all tracked test data.
func (e *RealTestEnv) cleanupTrackedEntities(ctx context.Context, t *testing.T) {
	for _, orgID := range e.CreatedOrgIDs {
		_, err := e.DB.Exec(ctx, "DELETE FROM organizations WHERE id = $1 OR public_id = $1", orgID)
		if err != nil {
			t.Logf("failed to delete org %s: %v", orgID, err)
		}
	}

	for _, userID := range e.CreatedUserIDs {
		_, err := e.DB.Exec(ctx, "DELETE FROM users WHERE id = $1 OR public_id = $1", userID)
		if err != nil {
			t.Logf("failed to delete user %s: %v", userID, err)
		}
	}
}

// TrackUser marks a user ID for cleanup after test completion.
func (e *RealTestEnv) TrackUser(publicID string) {
	e.CreatedUserIDs = append(e.CreatedUserIDs, publicID)
}

// TrackOrg marks an organization ID for cleanup after test completion.
func (e *RealTestEnv) TrackOrg(publicID string) {
	e.CreatedOrgIDs = append(e.CreatedOrgIDs, publicID)
}

// AddCleanup registers a cleanup function to be called on teardown.
func (e *RealTestEnv) AddCleanup(fn func()) {
	e.cleanup = append(e.cleanup, fn)
}

// ApplyTestEnvDefaults exports the scaffolded DB/Redis test settings for config.Load.
func ApplyTestEnvDefaults() (databaseURL, redisURL string) {
	db := resolveTestDBConfig()
	redisCfg := resolveTestRedisConfig()
	databaseURL = db.URL()
	redisURL = redisCfg.URL()

	setEnvDefault("APP_ENV", "test")
	_ = os.Setenv("TEST_DB_HOST", db.Host)
	_ = os.Setenv("TEST_DB_PORT", db.Port)
	_ = os.Setenv("TEST_DB_USER", db.User)
	_ = os.Setenv("TEST_DB_PASSWORD", db.Password)
	_ = os.Setenv("TEST_DB_NAME", db.Name)
	_ = os.Setenv("TEST_DATABASE_URL", databaseURL)
	_ = os.Setenv("DB_HOST", db.Host)
	_ = os.Setenv("DB_PORT", db.Port)
	_ = os.Setenv("DB_USER", db.User)
	_ = os.Setenv("DB_PASSWORD", db.Password)
	_ = os.Setenv("DB_NAME", db.Name)
	_ = os.Setenv("DATABASE_URL", databaseURL)

	_ = os.Setenv("TEST_REDIS_HOST", redisCfg.Host)
	_ = os.Setenv("TEST_REDIS_PORT", redisCfg.Port)
	_ = os.Setenv("TEST_REDIS_DB", redisCfg.DB)
	_ = os.Setenv("TEST_REDIS_URL", redisURL)
	_ = os.Setenv("REDIS_URL", redisURL)
	redisPrefix := firstNonEmpty(os.Getenv("TEST_REDIS_PREFIX"), defaultTestRedisPrefix)
	_ = os.Setenv("TEST_REDIS_PREFIX", redisPrefix)
	_ = os.Setenv("REDIS_PREFIX", redisPrefix)
	if redisCfg.Password != "" {
		_ = os.Setenv("TEST_REDIS_PASSWORD", redisCfg.Password)
		_ = os.Setenv("REDIS_PASSWORD", redisCfg.Password)
	} else {
		_ = os.Unsetenv("TEST_REDIS_PASSWORD")
		_ = os.Unsetenv("REDIS_PASSWORD")
	}

	return databaseURL, redisURL
}

// ResolveTestDatabaseURL constructs the effective test database URL.
func ResolveTestDatabaseURL() string {
	return resolveTestDBConfig().URL()
}

// ResolveTestRedisURL constructs the effective test Redis URL.
func ResolveTestRedisURL() string {
	return resolveTestRedisConfig().URL()
}

func resolveTestDBConfig() testDBConfig {
	if rawURL := firstNonEmpty(os.Getenv("TEST_DATABASE_URL")); rawURL != "" {
		if cfg, ok := parseTestDatabaseURL(rawURL); ok {
			return cfg
		}
	}

	return testDBConfig{
		Host:     normalizeHostForHostRun(firstNonEmpty(os.Getenv("TEST_DB_HOST"), "localhost")),
		Port:     firstNonEmpty(os.Getenv("TEST_DB_PORT"), os.Getenv("POSTGRES_TEST_PORT"), "5433"),
		User:     firstNonEmpty(os.Getenv("TEST_DB_USER"), os.Getenv("DB_USER"), "postgres"),
		Password: firstNonEmpty(os.Getenv("TEST_DB_PASSWORD"), os.Getenv("DB_PASSWORD"), "postgres"),
		Name:     firstNonEmpty(os.Getenv("TEST_DB_NAME"), defaultTestDatabaseName),
	}
}

func parseTestDatabaseURL(rawURL string) (testDBConfig, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return testDBConfig{}, false
	}

	user := firstNonEmpty(os.Getenv("TEST_DB_USER"), os.Getenv("DB_USER"), "postgres")
	password := firstNonEmpty(os.Getenv("TEST_DB_PASSWORD"), os.Getenv("DB_PASSWORD"), "postgres")
	if parsed.User != nil {
		user = firstNonEmpty(parsed.User.Username(), user)
		if parsedPassword, ok := parsed.User.Password(); ok {
			password = parsedPassword
		}
	}

	return testDBConfig{
		Host:     normalizeHostForHostRun(parsed.Hostname()),
		Port:     firstNonEmpty(parsed.Port(), "5432"),
		User:     user,
		Password: password,
		Name:     firstNonEmpty(strings.TrimPrefix(parsed.Path, "/"), defaultTestDatabaseName),
	}, true
}

func (cfg testDBConfig) URL() string {
	u := url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(cfg.User, cfg.Password),
		Host:   net.JoinHostPort(cfg.Host, cfg.Port),
		Path:   "/" + cfg.Name,
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

func resolveTestRedisConfig() testRedisConfig {
	if rawURL := firstNonEmpty(os.Getenv("TEST_REDIS_URL")); rawURL != "" {
		if cfg, ok := parseTestRedisURL(rawURL); ok {
			return cfg
		}
	}

	return testRedisConfig{
		Host:     normalizeHostForHostRun(firstNonEmpty(os.Getenv("TEST_REDIS_HOST"), "localhost")),
		Port:     firstNonEmpty(os.Getenv("TEST_REDIS_PORT"), "6380"),
		Password: firstNonEmpty(os.Getenv("TEST_REDIS_PASSWORD")),
		DB:       firstNonEmpty(os.Getenv("TEST_REDIS_DB"), "0"),
	}
}

func parseTestRedisURL(rawURL string) (testRedisConfig, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return testRedisConfig{}, false
	}

	password := ""
	if parsed.User != nil {
		password, _ = parsed.User.Password()
	}

	return testRedisConfig{
		Host:     normalizeHostForHostRun(parsed.Hostname()),
		Port:     firstNonEmpty(parsed.Port(), "6379"),
		Password: password,
		DB:       firstNonEmpty(strings.TrimPrefix(parsed.Path, "/"), "0"),
	}, true
}

func (cfg testRedisConfig) URL() string {
	u := url.URL{
		Scheme: "redis",
		Host:   net.JoinHostPort(cfg.Host, cfg.Port),
		Path:   "/" + cfg.DB,
	}
	if cfg.Password != "" {
		u.User = url.UserPassword("", cfg.Password)
	}
	return u.String()
}

func normalizeHostForHostRun(host string) string {
	switch strings.TrimSpace(host) {
	case "postgres", "db", "test-postgres", "{{PROJECT_NAME}}-postgres", "{{PROJECT_NAME}}-test-postgres":
		return "localhost"
	case "redis", "app-redis", "test-redis", "{{PROJECT_NAME}}-redis", "{{PROJECT_NAME}}-test-redis":
		return "localhost"
	default:
		return strings.TrimSpace(host)
	}
}

func skipOrFailInfra(t *testing.T, format string, args ...interface{}) {
	t.Helper()
	message := fmt.Sprintf(format, args...)
	if boolFromEnv("TEST_INFRA_REQUIRED") {
		t.Fatalf("%s", message)
	}
	t.Skipf("%s (skipping integration test)", message)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func setEnvDefault(key, value string) {
	if strings.TrimSpace(os.Getenv(key)) == "" {
		_ = os.Setenv(key, value)
	}
}

func boolFromEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

func intFromEnvOrDefault(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil && i > 0 {
			return i
		}
	}
	return defaultVal
}
