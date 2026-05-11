package database

import (
	"context"
	"testing"
)

func TestPostgresBulkHelpersValidateInputs(t *testing.T) {
	var db *PostgresDB
	if _, err := db.CopyFromRows(context.Background(), []string{"items"}, []string{"id"}, [][]any{{1}}); err == nil {
		t.Fatal("CopyFromRows nil db error = nil")
	}
	db = &PostgresDB{}
	if _, err := db.CopyFromRows(context.Background(), nil, []string{"id"}, [][]any{{1}}); err == nil {
		t.Fatal("CopyFromRows nil table path error = nil")
	}
	if err := db.SendBatch(context.Background(), nil, nil); err == nil {
		t.Fatal("SendBatch nil db pool error = nil")
	}
}
