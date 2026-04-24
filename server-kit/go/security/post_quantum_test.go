package security

import (
	"crypto/tls"
	"testing"
)

func TestApplyPostQuantumTLSAutoPrependsHybridKEM(t *testing.T) {
	cfg, err := ApplyPostQuantumTLS(&tls.Config{
		CurvePreferences: []tls.CurveID{tls.CurveP256},
	}, PostQuantumTLSAuto)
	if err != nil {
		t.Fatalf("ApplyPostQuantumTLS() error = %v", err)
	}
	if got := cfg.CurvePreferences[0]; got != tls.X25519MLKEM768 {
		t.Fatalf("first curve = %v, want %v", got, tls.X25519MLKEM768)
	}
	if !hasCurve(cfg.CurvePreferences, tls.X25519) {
		t.Fatalf("auto mode should retain classical X25519 fallback")
	}
}

func TestApplyPostQuantumTLSAutoLeavesNilDefaultsUntouched(t *testing.T) {
	cfg, err := ApplyPostQuantumTLS(nil, PostQuantumTLSAuto)
	if err != nil {
		t.Fatalf("ApplyPostQuantumTLS() error = %v", err)
	}
	if cfg.CurvePreferences != nil {
		t.Fatalf("auto mode should leave nil CurvePreferences so crypto/tls owns default ordering")
	}
}

func TestApplyPostQuantumTLSRequiredRemovesFallbackCurves(t *testing.T) {
	cfg, err := ApplyPostQuantumTLS(&tls.Config{
		CurvePreferences: []tls.CurveID{tls.CurveP256, tls.X25519},
	}, PostQuantumTLSRequired)
	if err != nil {
		t.Fatalf("ApplyPostQuantumTLS() error = %v", err)
	}
	if len(cfg.CurvePreferences) != 1 || cfg.CurvePreferences[0] != tls.X25519MLKEM768 {
		t.Fatalf("required mode curves = %v, want only X25519MLKEM768", cfg.CurvePreferences)
	}
}

func TestApplyPostQuantumTLSDisabledRemovesHybridKEM(t *testing.T) {
	cfg, err := ApplyPostQuantumTLS(&tls.Config{
		CurvePreferences: []tls.CurveID{tls.X25519MLKEM768, tls.X25519},
	}, PostQuantumTLSDisabled)
	if err != nil {
		t.Fatalf("ApplyPostQuantumTLS() error = %v", err)
	}
	if hasCurve(cfg.CurvePreferences, tls.X25519MLKEM768) {
		t.Fatalf("disabled mode should remove X25519MLKEM768")
	}
}

func TestApplyPostQuantumTLSDisabledOverridesNilDefaults(t *testing.T) {
	cfg, err := ApplyPostQuantumTLS(nil, PostQuantumTLSDisabled)
	if err != nil {
		t.Fatalf("ApplyPostQuantumTLS() error = %v", err)
	}
	if len(cfg.CurvePreferences) == 0 {
		t.Fatalf("disabled mode must set explicit classical curves to avoid Go default hybrid KEM")
	}
	if hasCurve(cfg.CurvePreferences, tls.X25519MLKEM768) {
		t.Fatalf("disabled mode should not include X25519MLKEM768")
	}
}
