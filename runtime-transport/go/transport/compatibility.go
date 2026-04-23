package transport

import (
	"fmt"
	"strings"
)

const EnvelopeSchemaVersion = "1.0"

var envelopeSchemaAliases = map[string]string{
	"v1": EnvelopeSchemaVersion,
}

type EnvelopeCompatibilityMatrix struct {
	Current   string            `json:"current"`
	Supported []string          `json:"supported"`
	Aliases   map[string]string `json:"aliases"`
}

func GetEnvelopeCompatibilityMatrix() EnvelopeCompatibilityMatrix {
	aliases := make(map[string]string, len(envelopeSchemaAliases))
	for key, value := range envelopeSchemaAliases {
		aliases[key] = value
	}
	return EnvelopeCompatibilityMatrix{
		Current:   EnvelopeSchemaVersion,
		Supported: []string{EnvelopeSchemaVersion},
		Aliases:   aliases,
	}
}

func NormalizeSchemaVersion(version string) string {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return EnvelopeSchemaVersion
	}
	if alias, ok := envelopeSchemaAliases[trimmed]; ok {
		return alias
	}
	return trimmed
}

func IsCompatibleSchemaVersion(version string) bool {
	return NormalizeSchemaVersion(version) == EnvelopeSchemaVersion
}

func ValidateSchemaVersion(version string) error {
	if IsCompatibleSchemaVersion(version) {
		return nil
	}
	return fmt.Errorf("unsupported envelope schema_version %q (supported: %s)", strings.TrimSpace(version), EnvelopeSchemaVersion)
}
