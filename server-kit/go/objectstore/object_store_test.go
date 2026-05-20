package objectstore

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

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

func TestMemoryStoreFilesPresignStageAndHelpers(t *testing.T) {
	store := New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "memory://objects",
		Bucket:   "bucket",
	})
	if desc := store.Describe(); desc["driver"] != "memory" || desc["bucket"] != "bucket" {
		t.Fatalf("Describe() = %+v", desc)
	}
	if got := (*Store)(nil).Describe(); len(got) != 0 {
		t.Fatalf("nil Describe() = %+v", got)
	}
	if _, err := (*Store)(nil).PutBytes(context.Background(), "k", nil, PutOptions{}); err == nil {
		t.Fatal("expected nil store PutBytes to fail")
	}
	if _, err := store.PutBytes(context.Background(), " ", nil, PutOptions{}); err == nil {
		t.Fatal("expected empty key to fail")
	}
	noBucket := New(runtimeconfig.ObjectStorageConfig{Endpoint: "memory://objects"})
	if _, err := noBucket.PutBytes(context.Background(), "k", nil, PutOptions{}); err == nil {
		t.Fatal("expected missing bucket to fail")
	}
	object, err := store.PutBytes(context.Background(), "/nested/../demo.txt", []byte("hello"), PutOptions{Metadata: map[string]string{" profile ": " test "}})
	if err != nil {
		t.Fatalf("PutBytes() error = %v", err)
	}
	if object.Key != "demo.txt" || object.ContentType != "application/octet-stream" || object.Metadata["profile"] != "test" {
		t.Fatalf("object = %+v", object)
	}
	if putURL, err := store.PresignPut(context.Background(), "demo.txt", "", time.Minute); err != nil || putURL == "" {
		t.Fatalf("PresignPut() = %q err=%v", putURL, err)
	}
	if getURL, err := store.PresignGet(context.Background(), "demo.txt", time.Minute); err != nil || getURL == "" {
		t.Fatalf("PresignGet() = %q err=%v", getURL, err)
	}
	if _, err := store.PresignGet(context.Background(), " ", time.Minute); err == nil {
		t.Fatal("expected empty presign key to fail")
	}
	if _, err := store.PresignPut(context.Background(), " ", "", time.Minute); err == nil {
		t.Fatal("expected empty presign put key to fail")
	}
	if _, err := store.ReadBytes(context.Background(), " "); err == nil {
		t.Fatal("expected empty read key to fail")
	}
	staged, cleanup, err := store.StageToTempFile(context.Background(), "demo.txt", "stage-*", "txt")
	if err != nil {
		t.Fatalf("StageToTempFile() error = %v", err)
	}
	defer func() { _ = cleanup() }()
	payload, err := os.ReadFile(staged)
	if err != nil || string(payload) != "hello" {
		t.Fatalf("staged payload = %q err=%v", string(payload), err)
	}
	if !strings.HasSuffix(staged, ".txt") {
		t.Fatalf("staged suffix = %s", staged)
	}
	if _, _, err := store.StageToTempFile(context.Background(), "missing", "", ""); err == nil {
		t.Fatal("expected missing object stage to fail")
	}

	tmp := t.TempDir() + "/upload.txt"
	if err := os.WriteFile(tmp, []byte("file"), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	fileObj, err := store.PutFile(context.Background(), "files/upload.txt", tmp, PutOptions{ContentType: "text/plain"})
	if err != nil || fileObj.Size != 4 || fileObj.ContentType != "text/plain" {
		t.Fatalf("PutFile() = %+v err=%v", fileObj, err)
	}
	if _, err := store.PutFile(context.Background(), " ", tmp, PutOptions{}); err == nil {
		t.Fatal("expected empty put file key to fail")
	}
	if _, err := store.PutFile(context.Background(), "x", "/missing/file", PutOptions{}); err == nil {
		t.Fatal("expected missing local file to fail")
	}
	if normalizeKey("./a/../b") != "b" || normalizeKey(".") != "" || !isMemoryEndpoint(" MEM://x ") {
		t.Fatal("objectstore helpers failed")
	}
	if ctxOrBackground(nilTestContext()) == nil || ctxOrBackground(context.Background()) == nil {
		t.Fatal("ctxOrBackground failed")
	}
}

func nilTestContext() context.Context {
	return nil
}

func TestObjectStoreURLsAndS3Validation(t *testing.T) {
	if got := (*Store)(nil).ObjectURL("key"); got != "" {
		t.Fatalf("nil ObjectURL = %q", got)
	}
	store := New(runtimeconfig.ObjectStorageConfig{
		Endpoint: "https://storage.example.com/root/",
		Bucket:   "bucket",
		Region:   "us-east-1",
	})
	if got := store.Describe(); got["driver"] != "s3-compatible" || got["endpoint"] != "https://storage.example.com/root/" {
		t.Fatalf("Describe() = %+v", got)
	}
	if got := store.ObjectURL("/a/../b.txt"); got != "https://storage.example.com/root/bucket/b.txt" {
		t.Fatalf("ObjectURL() = %q", got)
	}
	plain := New(runtimeconfig.ObjectStorageConfig{Endpoint: "localhost:9000", Bucket: "bucket"})
	if got := plain.ObjectURL("nested/file.txt"); got != "localhost:9000" {
		t.Fatalf("plain ObjectURL() = %q", got)
	}

	if _, err := (*Store)(nil).PresignPut(context.Background(), "key", "", time.Minute); err == nil {
		t.Fatalf("expected nil PresignPut error")
	}
	if _, err := (*Store)(nil).PresignGet(context.Background(), "key", time.Minute); err == nil {
		t.Fatalf("expected nil PresignGet error")
	}
	if _, err := (*Store)(nil).PutFile(context.Background(), "key", "missing", PutOptions{}); err == nil {
		t.Fatalf("expected nil PutFile error")
	}
	if _, err := (*Store)(nil).ReadBytes(context.Background(), "key"); err == nil {
		t.Fatalf("expected nil ReadBytes error")
	}
	if _, _, err := (*Store)(nil).StageToTempFile(context.Background(), "key", "", ""); err == nil {
		t.Fatalf("expected nil StageToTempFile error")
	}

	missingEndpoint := &Store{Bucket: "bucket", Region: "us-east-1", AccessKey: "a", SecretKey: "s"}
	if _, err := missingEndpoint.s3Client(); err == nil {
		t.Fatalf("expected missing endpoint error")
	}
	missingRegion := &Store{Endpoint: "https://storage.example.com", Bucket: "bucket", AccessKey: "a", SecretKey: "s"}
	if _, err := missingRegion.s3Client(); err == nil {
		t.Fatalf("expected missing region error")
	}
	missingCredentials := &Store{Endpoint: "https://storage.example.com", Bucket: "bucket", Region: "us-east-1"}
	if _, err := missingCredentials.s3Client(); err == nil {
		t.Fatalf("expected missing credentials error")
	}
	if _, err := missingCredentials.getPresignClient(); err == nil {
		t.Fatalf("expected missing presign credentials error")
	}
	if _, err := (&Store{Endpoint: "https://storage.example.com", Bucket: "bucket", Region: "us-east-1"}).PutBytes(context.Background(), "k", []byte("x"), PutOptions{}); err == nil {
		t.Fatalf("expected PutBytes s3 validation error")
	}
	if _, err := (&Store{Endpoint: "https://storage.example.com", Bucket: "bucket", Region: "us-east-1"}).ReadBytes(context.Background(), "k"); err == nil {
		t.Fatalf("expected ReadBytes s3 validation error")
	}
	if _, err := (&Store{Endpoint: "https://storage.example.com", Bucket: "bucket", Region: "us-east-1"}).PresignGet(context.Background(), "k", time.Minute); err == nil {
		t.Fatalf("expected PresignGet s3 validation error")
	}
}

func TestPresignClientUsesSeparateEndpointAndCaches(t *testing.T) {
	store := New(runtimeconfig.ObjectStorageConfig{
		Endpoint:        "https://storage.internal",
		PresignEndpoint: "https://storage.public",
		Bucket:          "bucket",
		Region:          "us-east-1",
		AccessKey:       "access",
		SecretKey:       "secret",
		UseTLS:          true,
	})
	client, err := store.getPresignClient()
	if err != nil {
		t.Fatalf("getPresignClient() error = %v", err)
	}
	if client == nil || store.presignClient == nil {
		t.Fatalf("expected presign client to be cached")
	}
	again, err := store.getPresignClient()
	if err != nil {
		t.Fatalf("second getPresignClient() error = %v", err)
	}
	if again != client {
		t.Fatalf("expected cached presign client")
	}
}
