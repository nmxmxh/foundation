package testutil

import (
	"context"
	"io"
	"strings"
	"testing"

	pgxmock "github.com/pashagolub/pgxmock/v4"
)

func TestMockStorageLifecycleAndFileIDNormalization(t *testing.T) {
	store := NewMockStorage()
	ctx := context.Background()

	url, err := store.Upload(ctx, strings.NewReader("document bytes"), "invoice.pdf", "application/pdf", map[string]string{"kind": "invoice"})
	if err != nil {
		t.Fatalf("upload mock file: %v", err)
	}
	if store.Count() != 1 {
		t.Fatalf("expected one stored file, got %d", store.Count())
	}
	if !strings.HasPrefix(url, "http://mock-storage/mock_file_") {
		t.Fatalf("unexpected mock URL: %s", url)
	}

	exists, err := store.Exists(ctx, url+"?download=1")
	if err != nil || !exists {
		t.Fatalf("expected uploaded file to exist via URL, exists=%v err=%v", exists, err)
	}

	body, err := store.Download(ctx, url)
	if err != nil {
		t.Fatalf("download mock file: %v", err)
	}
	defer func() {
		if closeErr := body.Close(); closeErr != nil {
			t.Fatalf("close mock body: %v", closeErr)
		}
	}()
	content, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read mock body: %v", err)
	}
	if string(content) != "document bytes" {
		t.Fatalf("unexpected content: %q", string(content))
	}

	gotURL, getErr := store.GetURL(ctx, "file-1")
	if getErr != nil || gotURL != "http://mock-storage/file-1" {
		t.Fatalf("unexpected get URL result: got=%q err=%v", gotURL, getErr)
	}
	deleteErr := store.Delete(ctx, url)
	if deleteErr != nil {
		t.Fatalf("delete mock file: %v", deleteErr)
	}
	exists, err = store.Exists(ctx, url)
	if err != nil || exists {
		t.Fatalf("expected deleted file to be absent, exists=%v err=%v", exists, err)
	}
	if _, err := store.Download(ctx, url); err == nil {
		t.Fatalf("expected missing download to fail")
	}

	store.Files["manual"] = []byte("x")
	store.Clear()
	if store.Count() != 0 {
		t.Fatalf("expected clear to remove all files")
	}
}

func TestTestEnvURLResolutionAndDefaults(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", "postgresql://custom:secret@test-postgres:15432/{{PROJECT_NAME}}_custom?sslmode=disable")
	t.Setenv("TEST_REDIS_URL", "redis://:redis-secret@{{PROJECT_NAME}}-test-redis:16379/4")
	t.Setenv("APP_ENV", "")

	databaseURL, redisURL := ApplyTestEnvDefaults()
	if !strings.Contains(databaseURL, "custom:secret@localhost:15432") || !strings.Contains(databaseURL, "/{{PROJECT_NAME}}_custom") {
		t.Fatalf("unexpected database URL: %s", databaseURL)
	}
	if redisURL != "redis://:redis-secret@localhost:16379/4" {
		t.Fatalf("unexpected redis URL: %s", redisURL)
	}
	if got := ResolveTestDatabaseURL(); !strings.Contains(got, "custom:secret@localhost:15432") {
		t.Fatalf("ResolveTestDatabaseURL did not preserve parsed config: %s", got)
	}
	if got := ResolveTestRedisURL(); got != "redis://:redis-secret@localhost:16379/4" {
		t.Fatalf("ResolveTestRedisURL did not preserve parsed config: %s", got)
	}
	if got := firstNonEmpty("", "  ", "value"); got != "value" {
		t.Fatalf("firstNonEmpty returned %q", got)
	}
}

func TestTestEnvFallbackHelpers(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", "://bad")
	t.Setenv("TEST_REDIS_URL", "://bad")
	t.Setenv("TEST_DB_HOST", "postgres")
	t.Setenv("TEST_DB_PORT", "5439")
	t.Setenv("TEST_DB_USER", "db-user")
	t.Setenv("TEST_DB_PASSWORD", "db-pass")
	t.Setenv("TEST_DB_NAME", "db-name")
	t.Setenv("TEST_REDIS_HOST", "redis")
	t.Setenv("TEST_REDIS_PORT", "6399")
	t.Setenv("TEST_REDIS_PASSWORD", "")
	t.Setenv("TEST_REDIS_DB", "2")
	t.Setenv("BOOL_TRUE", "yes")
	t.Setenv("BOOL_FALSE", "no")
	t.Setenv("INT32_OK", "17")
	t.Setenv("INT32_BAD", "-1")

	db := resolveTestDBConfig()
	if db.Host != "localhost" || db.Port != "5439" || db.User != "db-user" || db.Password != "db-pass" || db.Name != "db-name" {
		t.Fatalf("unexpected fallback db config: %#v", db)
	}
	redis := resolveTestRedisConfig()
	if redis.Host != "localhost" || redis.Port != "6399" || redis.DB != "2" {
		t.Fatalf("unexpected fallback redis config: %#v", redis)
	}
	if !boolFromEnv("BOOL_TRUE") || boolFromEnv("BOOL_FALSE") {
		t.Fatalf("boolean env parsing failed")
	}
	if safeInt32FromEnvOrDefault("INT32_OK", 5) != 17 || safeInt32FromEnvOrDefault("INT32_BAD", 5) != 5 {
		t.Fatalf("safe int32 parsing failed")
	}
	if normalizeHostForHostRun(" custom-host ") != "custom-host" {
		t.Fatalf("host normalization failed")
	}

	mustSetenv("DOCUOS_TEST_UNSET", "present")
	if got := firstNonEmpty(""); got != "" {
		t.Fatalf("expected empty first non-empty result, got %q", got)
	}
	mustUnsetenv("DOCUOS_TEST_UNSET")
	if got := strings.TrimSpace(firstNonEmpty("")); got != "" {
		t.Fatalf("expected unset helper to leave no fallback value, got %q", got)
	}
}

func TestSetupTestEnvCleanupAndExpectations(t *testing.T) {
	env := SetupTestEnv(t)
	called := false
	env.cleanup = append(env.cleanup, func() { called = true })

	env.DB.ExpectQuery(`SELECT 1`).WillReturnRows(pgxmock.NewRows([]string{"one"}).AddRow(1))
	var one int
	if err := env.DB.QueryRow(context.Background(), "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("scan mock query: %v", err)
	}
	if one != 1 {
		t.Fatalf("unexpected mock query value: %d", one)
	}
	env.ExpectationsWereMet(t)
	env.Teardown()
	if !called {
		t.Fatalf("expected registered cleanup to run")
	}
}

func TestRealTestEnvTrackingHelpersWithoutConnections(t *testing.T) {
	env := &RealTestEnv{}
	env.TrackUser("user-public")
	env.TrackOrg("org-public")

	if len(env.CreatedUserIDs) != 1 || len(env.CreatedOrgIDs) != 1 {
		t.Fatalf("tracked ids not recorded: users=%#v orgs=%#v", env.CreatedUserIDs, env.CreatedOrgIDs)
	}
}

func TestRealTestEnvTeardownRunsCleanupHooksWithoutConnections(t *testing.T) {
	env := &RealTestEnv{}
	called := false
	env.AddCleanup(func() { called = true })
	env.Teardown(t)
	if !called {
		t.Fatalf("expected real env cleanup hook to run")
	}
}
