package database

import (
	"errors"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
)

func TestMarshalJSONDefaultsNilToObject(t *testing.T) {
	raw, err := MarshalJSON(nil)
	if err != nil {
		t.Fatalf("MarshalJSON(nil) error = %v", err)
	}
	if string(raw) != "{}" {
		t.Fatalf("MarshalJSON(nil) = %s, want {}", raw)
	}
}

func TestUnmarshalJSONDefaultsEmptyToObject(t *testing.T) {
	var got map[string]any
	if err := UnmarshalJSON(nil, &got); err != nil {
		t.Fatalf("UnmarshalJSON(nil) error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("UnmarshalJSON(nil) = %#v, want empty object", got)
	}
}

func TestMarshalJSONBReturnsValidationDomainError(t *testing.T) {
	_, err := MarshalJSONB(func() {}, "bad_json", "payload must be JSON")
	if err == nil {
		t.Fatal("MarshalJSONB(non-json) error = nil")
	}
	if !errors.Is(err, &domainerr.Error{Kind: domainerr.KindValidation}) {
		t.Fatalf("MarshalJSONB(non-json) error = %v, want validation domain error", err)
	}
}

func TestUnmarshalJSONBReturnsInternalDomainError(t *testing.T) {
	var got map[string]any
	err := UnmarshalJSONB([]byte("{"), &got, "bad_json", "payload must decode")
	if err == nil {
		t.Fatal("UnmarshalJSONB(malformed) error = nil")
	}
	if !errors.Is(err, &domainerr.Error{Kind: domainerr.KindInternal}) {
		t.Fatalf("UnmarshalJSONB(malformed) error = %v, want internal domain error", err)
	}
}

func TestNormalizePageBounds(t *testing.T) {
	limit, offset := NormalizePageBounds(-1, -5, 20, 50)
	if limit != 20 || offset != 0 {
		t.Fatalf("NormalizePageBounds negative = (%d, %d), want (20, 0)", limit, offset)
	}

	limit, offset = NormalizePageBounds(500, 9, 20, 50)
	if limit != 50 || offset != 9 {
		t.Fatalf("NormalizePageBounds max = (%d, %d), want (50, 9)", limit, offset)
	}
}
