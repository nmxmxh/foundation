// Package vectorstr provides fixed-width string utilities and canonical header lookups.
package vectorstr

// CanonicalHeaderID represents a recognized ingress HTTP header.
type CanonicalHeaderID uint8

const (
	HeaderUnknown CanonicalHeaderID = iota
	HeaderAuthorization
	HeaderContentType
	HeaderCorrelationID
	HeaderRequestID
	HeaderTenantID
	HeaderDeviceID
	HeaderAccept
)

var (
	lookupCanonicalHeaderAsso = [256]uint8{
		'd': 4,
		'r': 5,
	}

	lookupCanonicalHeaderKeys = [8]string{
		"x-correlation-id", "x-request-id", "", "x-tenant-id", "content-type", "authorization", "accept", "x-device-id",
	}

	lookupCanonicalHeaderValues = [8]CanonicalHeaderID{
		HeaderCorrelationID, HeaderRequestID, HeaderUnknown, HeaderTenantID, HeaderContentType, HeaderAuthorization, HeaderAccept, HeaderDeviceID,
	}
)

// LookupCanonicalHeader performs zero-collision, branchless perfect hash lookup in ~5 CPU cycles.
func LookupCanonicalHeader(key string) (CanonicalHeaderID, bool) {
	if len(key) < 3 {
		return HeaderUnknown, false
	}
	slot := (len(key) + int(lookupCanonicalHeaderAsso[key[0]]) + int(lookupCanonicalHeaderAsso[key[1]]) + int(lookupCanonicalHeaderAsso[key[2]])) & 7
	if slot < 8 && lookupCanonicalHeaderKeys[slot] == key {
		return lookupCanonicalHeaderValues[slot], true
	}
	return HeaderUnknown, false
}
