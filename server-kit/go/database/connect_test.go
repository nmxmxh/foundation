package database

import (
	"context"
	"testing"
)

func TestConnectMemoryDriver(t *testing.T) {
	store, err := Connect(context.Background(), "", DriverMemory)
	if err != nil {
		t.Fatalf("connect memory driver failed: %v", err)
	}
	if store == nil {
		t.Fatalf("expected runtime store")
	}
	store.Close()
}

func TestConnectUnknownDriverDefaultsToMemory(t *testing.T) {
	store, err := Connect(context.Background(), "", "unknown")
	if err != nil {
		t.Fatalf("connect unknown driver failed: %v", err)
	}
	if store == nil {
		t.Fatalf("expected runtime store")
	}
	store.Close()
}

func TestConnectPostgresRequiresURL(t *testing.T) {
	_, err := Connect(context.Background(), "", DriverPostgres)
	if err == nil {
		t.Fatalf("expected error for postgres driver without url")
	}
}
