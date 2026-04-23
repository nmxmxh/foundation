package database

import (
	"context"
	"testing"
)

func TestMemoryDBUpsertGetListCount(t *testing.T) {
	db := NewMemoryDB()

	_, err := db.UpsertRecord(context.Background(), DomainRecord{
		Domain:         "workspace",
		Collection:     "brand_kits",
		OrganizationID: "org_1",
		RecordID:       "brand_1",
		Data: map[string]any{
			"brand_kit_id": "brand_1",
			"workspace_id": "ws_1",
			"locale_code":  "en-US",
		},
	})
	if err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	rec, ok, err := db.GetRecord(context.Background(), "workspace", "brand_kits", "org_1", "brand_1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !ok || rec.Data["brand_kit_id"] != "brand_1" {
		t.Fatalf("expected record to be retrievable")
	}

	items, err := db.ListRecords(context.Background(), "workspace", "brand_kits", "org_1", map[string]any{"workspace_id": "ws_1"}, 10)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one listed record")
	}

	count, err := db.CountRecords(context.Background(), "workspace", "brand_kits", "org_1", map[string]any{"locale_code": "en-US"})
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count=1")
	}
}

func TestMemoryDBContextCancel(t *testing.T) {
	db := NewMemoryDB()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := db.UpsertRecord(ctx, DomainRecord{
		Domain:         "identity",
		Collection:     "users",
		OrganizationID: "org_1",
		RecordID:       "usr_1",
		Data:           map[string]any{"user_id": "usr_1"},
	}); err == nil {
		t.Fatalf("expected context canceled error")
	}
}
