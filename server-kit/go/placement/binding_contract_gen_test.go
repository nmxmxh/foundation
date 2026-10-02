package placement

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func bindingGolden(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../../runtime-sdk/protocols/system/v1/testdata/binding_" + name + ".hex")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestBindingCompilerVectors(t *testing.T) {
	wire := bindingGolden(t, "request")
	request, err := DecodeRuntimeBindingRequest(wire)
	if err != nil || request.RegistryID != ^uint64(0) || request.BindingID != 9007199254740993 || request.OutputCapacity != 8 || request.TimeoutMillis != 30000 {
		t.Fatalf("compiler request: %+v %v", request, err)
	}
	encoded := make([]byte, RuntimeBindingRequestBytes)
	if err := request.EncodeTo(encoded); err != nil || !bytes.Equal(encoded, wire) {
		t.Fatalf("compiler request mismatch: %v", err)
	}
	wire = bindingGolden(t, "receipt")
	receipt, err := DecodeRuntimeBindingReceipt(wire)
	if err != nil || receipt.OutputBytes != 8 || receipt.TransferredBytes != 184 || receipt.BindingRevision != 3 {
		t.Fatalf("compiler receipt: %+v %v", receipt, err)
	}
	encoded = make([]byte, RuntimeBindingReceiptBytes)
	if err := receipt.EncodeTo(encoded); err != nil || !bytes.Equal(encoded, wire) {
		t.Fatalf("compiler receipt mismatch: %v", err)
	}
	if err := request.EncodeTo(nil); err == nil {
		t.Fatal("short request accepted")
	}
	if err := receipt.EncodeTo(nil); err == nil {
		t.Fatal("short receipt accepted")
	}
	for n := 0; n < RuntimeBindingRequestBytes; n++ {
		if _, err := DecodeRuntimeBindingRequest(make([]byte, n)); err == nil {
			t.Fatal(n)
		}
	}
	for n := 0; n < RuntimeBindingReceiptBytes; n++ {
		if _, err := DecodeRuntimeBindingReceipt(make([]byte, n)); err == nil {
			t.Fatal(n)
		}
	}
}

func TestBindingRegistryConfiguration(t *testing.T) {
	authorize := func(context.Context, BindingAction, uint64, uint64) error { return nil }
	valid := BindingType{1, func([]byte) error { return nil }}
	limits := BindingLimits{1, 1, 8, 1}
	if _, err := NewBindingRegistry(0, limits, authorize, valid); err == nil {
		t.Fatal("zero registry accepted")
	}
	if _, err := NewBindingRegistry(1, limits, nil, valid); err == nil {
		t.Fatal("missing authorization accepted")
	}
	for _, types := range [][]BindingType{nil, {{0, valid.Validate}}, {{1, nil}}, {valid, valid}, make([]BindingType, 257)} {
		if _, err := NewBindingRegistry(1, limits, authorize, types...); err == nil {
			t.Fatal("invalid type registry accepted")
		}
	}
	for _, mutate := range []func(*BindingLimits){
		func(v *BindingLimits) { v.Resources = 0 }, func(v *BindingLimits) { v.Resources = 65537 },
		func(v *BindingLimits) { v.Bindings = 0 }, func(v *BindingLimits) { v.Bindings = 65537 },
		func(v *BindingLimits) { v.ResidentBytes = 0 }, func(v *BindingLimits) { v.ResidentBytes = 512<<20 + 1 },
		func(v *BindingLimits) { v.InFlight = 0 }, func(v *BindingLimits) { v.InFlight = 65537 },
	} {
		v := limits
		mutate(&v)
		if _, err := NewBindingRegistry(1, v, authorize, valid); err == nil {
			t.Fatal(v)
		}
	}
}

func FuzzBindingRequest(f *testing.F) {
	f.Add(make([]byte, RuntimeBindingRequestBytes))
	f.Fuzz(func(t *testing.T, wire []byte) {
		request, err := DecodeRuntimeBindingRequest(wire)
		if err != nil {
			return
		}
		encoded := make([]byte, RuntimeBindingRequestBytes)
		if err := request.EncodeTo(encoded); err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeRuntimeBindingRequest(encoded)
		if err != nil || decoded != request {
			t.Fatal("round trip changed request")
		}
	})
}
