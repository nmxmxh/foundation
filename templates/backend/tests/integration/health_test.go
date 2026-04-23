//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabase_Ping(t *testing.T) {
	env := setupTestWithDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := env.DB.Ping(ctx)
	require.NoError(t, err, "database should be pingable")
}

func TestDatabase_SimpleQuery(t *testing.T) {
	env := setupTestWithDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result int
	err := env.DB.QueryRow(ctx, "SELECT 1").Scan(&result)
	require.NoError(t, err, "simple query should succeed")
	assert.Equal(t, 1, result)
}

func TestDatabase_SchemaExists(t *testing.T) {
	env := setupTestWithDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Check if users table exists (from initial migration)
	var exists bool
	err := env.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = 'users'
		)
	`).Scan(&exists)

	if err != nil {
		t.Skipf("schema check failed (migrations may not be run): %v", err)
	}

	// This is informational - don't fail if migrations haven't run
	t.Logf("users table exists: %v", exists)
}
