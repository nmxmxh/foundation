package objectstore

import (
	"context"
	"testing"

	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
)

func TestMemoryStoreRoundTripsBytes(t *testing.T) {
	store := New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://object-tests",
		Bucket:   "reframe-test",
		Strict:   true,
	})

	object, err := store.PutBytes(context.Background(), "variants/demo.bin", []byte("rendered"), PutOptions{
		ContentType: "application/octet-stream",
		Metadata:    map[string]string{"profile": "video_vertical_primary"},
	})
	if err != nil {
		t.Fatalf("PutBytes() error = %v", err)
	}
	if object.URL == "" {
		t.Fatal("expected object url")
	}

	payload, err := store.ReadBytes(context.Background(), "variants/demo.bin")
	if err != nil {
		t.Fatalf("ReadBytes() error = %v", err)
	}
	if string(payload) != "rendered" {
		t.Fatalf("unexpected payload %q", string(payload))
	}
}
