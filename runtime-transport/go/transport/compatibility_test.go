package transport

import "testing"

func TestEnvelopeSchemaCompatibility(t *testing.T) {
	cases := map[string]string{
		"":      EnvelopeSchemaVersion,
		" 1.0 ": EnvelopeSchemaVersion,
		"v1":    EnvelopeSchemaVersion,
		"2.0":   "2.0",
	}
	for input, want := range cases {
		if got := NormalizeSchemaVersion(input); got != want {
			t.Fatalf("NormalizeSchemaVersion(%q) = %q, want %q", input, got, want)
		}
	}
	if !IsCompatibleSchemaVersion("v1") {
		t.Fatal("expected v1 alias to be compatible")
	}
	if err := ValidateSchemaVersion("1.0"); err != nil {
		t.Fatalf("ValidateSchemaVersion(1.0) error = %v", err)
	}
	if err := ValidateSchemaVersion("2.0"); err == nil {
		t.Fatal("expected unsupported schema version to fail")
	}
}

func TestEnvelopeCompatibilityMatrixCopiesAliases(t *testing.T) {
	matrix := GetEnvelopeCompatibilityMatrix()
	if matrix.Current != EnvelopeSchemaVersion {
		t.Fatalf("Current = %q, want %q", matrix.Current, EnvelopeSchemaVersion)
	}
	if len(matrix.Supported) != 1 || matrix.Supported[0] != EnvelopeSchemaVersion {
		t.Fatalf("Supported = %+v", matrix.Supported)
	}
	matrix.Aliases["v1"] = "mutated"
	if GetEnvelopeCompatibilityMatrix().Aliases["v1"] != EnvelopeSchemaVersion {
		t.Fatal("expected aliases to be copied defensively")
	}
}
