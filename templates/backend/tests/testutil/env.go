package testutil

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pgxmock "github.com/pashagolub/pgxmock/v4"
	"github.com/redis/go-redis/v9"
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
// This is intended for integration tests and will skip if the database is unavailable.
func SetupRealTestEnv(t *testing.T) *RealTestEnv {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	dbURL := ResolveTestDatabaseURL()
	redisURL := ResolveTestRedisURL()

	// Setup database pool
	dbConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Skipf("unable to parse database URL (skipping integration test): %v", err)
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
		t.Skipf("unable to connect to database (skipping integration test): %v", err)
	}

	if err := dbPool.Ping(ctx); err != nil {
		dbPool.Close()
		t.Skipf("unable to ping database (skipping integration test): %v", err)
	}

	// Setup Redis (optional - skip if unavailable)
	var redisClient *redis.Client
	if redisURL != "" {
		opts, err := redis.ParseURL(redisURL)
		if err == nil {
			redisClient = redis.NewClient(opts)
			if err := redisClient.Ping(ctx).Err(); err != nil {
				redisClient.Close()
				redisClient = nil
				t.Logf("redis unavailable, continuing without it: %v", err)
			}
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

	// Execute cleanup functions in reverse order
	for i := len(e.cleanup) - 1; i >= 0; i-- {
		e.cleanup[i]()
	}

	// Clean up tracked entities
	e.cleanupTrackedEntities(ctx, t)

	// Close connections
	if e.Redis != nil {
		_ = e.Redis.Close()
	}
	if e.DB != nil {
		e.DB.Close()
	}
}

// cleanupTrackedEntities deletes all tracked test data.
func (e *RealTestEnv) cleanupTrackedEntities(ctx context.Context, t *testing.T) {
	// Delete organizations first (may have FK constraints to users)
	for _, orgID := range e.CreatedOrgIDs {
		_, err := e.DB.Exec(ctx, "DELETE FROM organizations WHERE id = $1 OR public_id = $1", orgID)
		if err != nil {
			t.Logf("failed to delete org %s: %v", orgID, err)
		}
	}

	// Delete users
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

// ResolveTestDatabaseURL constructs the test database URL from environment variables.
func ResolveTestDatabaseURL() string {
	host, port, user, password, name := resolveTestDBConfig()
	userinfo := url.UserPassword(user, password)
	return fmt.Sprintf("postgresql://%s@%s:%s/%s?sslmode=disable", userinfo.String(), host, port, name)
}

// ResolveTestRedisURL constructs the test Redis URL from environment variables.
func ResolveTestRedisURL() string {
	host := firstNonEmpty(os.Getenv("TEST_REDIS_HOST"), os.Getenv("REDIS_HOST"), "localhost")
	port := firstNonEmpty(os.Getenv("TEST_REDIS_PORT"), os.Getenv("REDIS_PORT"), "6379")
	password := firstNonEmpty(os.Getenv("TEST_REDIS_PASSWORD"), os.Getenv("REDIS_PASSWORD"), "")

	if host == "redis" {
		host = "localhost"
	}

	if password != "" {
		return fmt.Sprintf("redis://:%s@%s:%s/0", password, host, port)
	}
	return fmt.Sprintf("redis://%s:%s/0", host, port)
}

func resolveTestDBConfig() (host, port, user, password, name string) {
	user = firstNonEmpty(os.Getenv("TEST_DB_USER"), os.Getenv("DB_USER"), "postgres")
	password = firstNonEmpty(os.Getenv("TEST_DB_PASSWORD"), os.Getenv("DB_PASSWORD"), "postgres")
	name = firstNonEmpty(os.Getenv("TEST_DB_NAME"), os.Getenv("DB_NAME"), "test_db")

	host = firstNonEmpty(os.Getenv("TEST_DB_HOST"), os.Getenv("DB_HOST"), "localhost")
	// Docker service hostnames are remapped for host-machine tests
	if host == "postgres" || host == "db" {
		host = "localhost"
	}

	port = firstNonEmpty(os.Getenv("TEST_DB_PORT"), os.Getenv("DB_PORT"), "5432")
	return host, port, user, password, name
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func intFromEnvOrDefault(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}
