package security

import (
	"crypto/tls"
	"fmt"
)

type PostQuantumTLSMode string

const (
	PostQuantumTLSAuto     PostQuantumTLSMode = "auto"
	PostQuantumTLSRequired PostQuantumTLSMode = "required"
	PostQuantumTLSDisabled PostQuantumTLSMode = "disabled"
)

// ApplyPostQuantumTLS configures TLS key exchange posture without moving
// post-quantum work into request handlers or render/runtime hot paths.
func ApplyPostQuantumTLS(base *tls.Config, mode PostQuantumTLSMode) (*tls.Config, error) {
	cfg := &tls.Config{}
	if base != nil {
		cfg = base.Clone()
	}

	switch mode {
	case "", PostQuantumTLSAuto:
		if len(cfg.CurvePreferences) == 0 {
			return cfg, nil
		}
		cfg.CurvePreferences = prependCurve(cfg.CurvePreferences, tls.X25519MLKEM768)
		if !hasCurve(cfg.CurvePreferences, tls.X25519) {
			cfg.CurvePreferences = append(cfg.CurvePreferences, tls.X25519)
		}
		return cfg, nil
	case PostQuantumTLSRequired:
		cfg.CurvePreferences = []tls.CurveID{tls.X25519MLKEM768}
		return cfg, nil
	case PostQuantumTLSDisabled:
		cfg.CurvePreferences = removeCurve(cfg.CurvePreferences, tls.X25519MLKEM768)
		if len(cfg.CurvePreferences) == 0 {
			cfg.CurvePreferences = []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521}
		}
		return cfg, nil
	default:
		return nil, fmt.Errorf("unsupported post-quantum TLS mode %q", mode)
	}
}

func prependCurve(curves []tls.CurveID, curve tls.CurveID) []tls.CurveID {
	if hasCurve(curves, curve) {
		return curves
	}
	next := make([]tls.CurveID, 0, len(curves)+1)
	next = append(next, curve)
	next = append(next, curves...)
	return next
}

func removeCurve(curves []tls.CurveID, curve tls.CurveID) []tls.CurveID {
	next := curves[:0]
	for _, item := range curves {
		if item != curve {
			next = append(next, item)
		}
	}
	return next
}

func hasCurve(curves []tls.CurveID, curve tls.CurveID) bool {
	for _, item := range curves {
		if item == curve {
			return true
		}
	}
	return false
}
