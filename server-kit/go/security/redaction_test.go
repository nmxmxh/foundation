package security

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeSessionIdentifierRedactsJWTs(t *testing.T) {
	jwt := "header.payload.signature"
	assert.Equal(t, RedactSecret(jwt), SanitizeSessionIdentifier(jwt))
	assert.Equal(t, "session_live", SanitizeSessionIdentifier("session_live"))
	assert.Equal(t, "opaque-session", SanitizeSessionIdentifier("opaque-session"))
}

func TestSanitizeTelemetryValueRecursivelyRedactsSensitiveFields(t *testing.T) {
	sanitized := SanitizeTelemetryValue(
		map[string]any{
			"authorization": "Bearer top-secret",
			"nested": map[string]any{
				"auth_token": "opaque-secret",
				"session_id": "header.payload.signature",
			},
		},
		"",
	).(map[string]any)

	assert.Equal(t, RedactSecret("Bearer top-secret"), sanitized["authorization"])
	nested := sanitized["nested"].(map[string]any)
	assert.Equal(t, RedactSecret("opaque-secret"), nested["auth_token"])
	assert.Equal(t, RedactSecret("header.payload.signature"), nested["session_id"])
}

func TestHashIdentifierIgnoresWhitespace(t *testing.T) {
	assert.Equal(t, HashIdentifier(" secret "), HashIdentifier("secret"))
	assert.NotEmpty(t, HashIdentifier("secret"))
	assert.Empty(t, HashIdentifier("   "))
}
