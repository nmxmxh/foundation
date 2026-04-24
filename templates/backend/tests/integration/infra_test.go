//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIntegrationInfrastructure_Database(t *testing.T) {
	env := setupTestWithDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, env.DB.Ping(ctx), "database should be pingable")

	var result int
	err := env.DB.QueryRow(ctx, "SELECT 1").Scan(&result)
	require.NoError(t, err, "simple query should succeed")
	require.Equal(t, 1, result)
}

func TestIntegrationInfrastructure_Redis(t *testing.T) {
	env := setupTestWithDB(t)
	if env.Redis == nil {
		t.Skip("redis unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, env.Redis.Ping(ctx).Err(), "redis should be pingable")

	key := fmt.Sprintf("%sintegration:%d", os.Getenv("REDIS_PREFIX"), time.Now().UnixNano())
	t.Cleanup(func() {
		_ = env.Redis.Del(context.Background(), key).Err()
	})

	require.NoError(t, env.Redis.Set(ctx, key, "ok", time.Minute).Err())
	value, err := env.Redis.Get(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, "ok", value)
}
