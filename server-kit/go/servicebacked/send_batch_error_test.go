//go:build servicebacked

package servicebacked

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TE-20 regression for a defect repaired on 2026-08-25.
//
// PostgresDB.SendBatch closed its pgx BatchResults in a deferred call and threw
// the returned error away. pgx reports the first failure among results the
// consumer did not read through Close, so a batch containing a failing
// statement returned nil whenever the consumer stopped reading before reaching
// it — and the database operation metric recorded the call as a success. The
// caller then proceeded on the belief that every statement had committed.
//
// The consumer below deliberately reads nothing, which is the exact shape that
// used to hide the failure: without Close being consulted there is no other
// channel through which the bad statement can be reported.
func TestServiceBackedSendBatchSurfacesUnreadStatementErrors(t *testing.T) {
	env := requireServiceEnv(t)
	ctx := context.Background()
	store := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer store.Close()
	db := requirePostgresDB(t, store)

	err := db.SendBatch(ctx, func(batch *pgx.Batch) {
		// Syntactically valid, guaranteed to fail at execution: division by
		// zero is a runtime error rather than a parse error, so the failure
		// arrives with the result rather than when the batch is built.
		batch.Queue("SELECT 1 / 0")
	}, func(pgx.BatchResults) error {
		// Reads nothing and reports success, exactly as a consumer that only
		// cares about statements it queued earlier would.
		return nil
	})

	if err == nil {
		t.Fatal("SendBatch reported success for a batch containing a failing statement; " +
			"the error from BatchResults.Close was dropped")
	}
}

// TestServiceBackedSendBatchStillReportsConsumerErrors pins the other half of
// the contract: an error the consumer returns itself must not be replaced by
// whatever Close reports afterwards.
func TestServiceBackedSendBatchStillReportsConsumerErrors(t *testing.T) {
	env := requireServiceEnv(t)
	ctx := context.Background()
	store := openPostgres(t, env, serviceBackedPoolOptions(4))
	defer store.Close()
	db := requirePostgresDB(t, store)

	sentinel := errSendBatchConsumer
	err := db.SendBatch(ctx, func(batch *pgx.Batch) {
		batch.Queue("SELECT 1")
	}, func(pgx.BatchResults) error {
		return sentinel
	})

	if err == nil {
		t.Fatal("SendBatch swallowed the consumer's error")
	}
}

type sendBatchConsumerError struct{}

func (sendBatchConsumerError) Error() string { return "consumer refused the batch" }

var errSendBatchConsumer = sendBatchConsumerError{}
