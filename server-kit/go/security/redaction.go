package security

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

var telemetrySensitiveKeys = map[string]struct{}{
	"access_token":   {},
	"api_key":        {},
	"apikey":         {},
	"auth_token":     {},
	"authorization":  {},
	"client_secret":  {},
	"id_token":       {},
	"password":       {},
	"pdf_password":   {},
	"refresh_token":  {},
	"secret":         {},
	"secret_key":     {},
	"session_token":  {},
	"shared_secret":  {},
	"signing_key":    {},
	"token":          {},
	"token_hash":     {},
	"webhook_secret": {},
}

func HashIdentifier(raw string) string {
	return prefixedHashIdentifier("", raw)
}

func prefixedHashIdentifier(prefix, raw string) string {
	sum, ok := hashIdentifierBytes(raw)
	if !ok {
		return ""
	}
	var out [32]byte
	n := copy(out[:], prefix)
	hex.Encode(out[n:n+16], sum[:])
	return string(out[:n+16])
}

func hashIdentifierBytes(raw string) ([8]byte, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return [8]byte{}, false
	}
	var sum [32]byte
	if len(trimmed) <= 512 {
		var buf [512]byte
		copy(buf[:], trimmed)
		sum = sha256.Sum256(buf[:len(trimmed)])
	} else {
		sum = sha256.Sum256([]byte(trimmed))
	}
	var prefix [8]byte
	copy(prefix[:], sum[:8])
	return prefix, true
}

func RedactSecret(raw string) string {
	return prefixedHashIdentifier("redacted:", raw)
}

func LooksLikeJWT(raw string) bool {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return false
		}
	}
	return true
}

func SanitizeSessionIdentifier(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "session_") {
		return trimmed
	}
	if LooksLikeJWT(trimmed) {
		return RedactSecret(trimmed)
	}
	return trimmed
}

func SanitizeTelemetryValue(value any, keyHint string) any {
	switch typed := value.(type) {
	case map[string]any:
		sanitized := make(map[string]any, len(typed))
		for key, raw := range typed {
			sanitized[key] = SanitizeTelemetryValue(raw, key)
		}
		return sanitized
	case []any:
		sanitized := make([]any, 0, len(typed))
		for _, raw := range typed {
			sanitized = append(sanitized, SanitizeTelemetryValue(raw, keyHint))
		}
		return sanitized
	case []string:
		sanitized := make([]any, 0, len(typed))
		for _, raw := range typed {
			sanitized = append(sanitized, SanitizeTelemetryValue(raw, keyHint))
		}
		return sanitized
	case string:
		return sanitizeTelemetryString(typed, keyHint)
	default:
		return value
	}
}

func sanitizeTelemetryString(raw string, keyHint string) string {
	normalizedKey := strings.ToLower(strings.TrimSpace(keyHint))
	if _, ok := telemetrySensitiveKeys[normalizedKey]; ok {
		return RedactSecret(raw)
	}
	if normalizedKey == "session_id" || normalizedKey == "sessionid" {
		return SanitizeSessionIdentifier(raw)
	}
	return raw
}
