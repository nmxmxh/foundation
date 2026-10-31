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

//go:generate node ../../../tooling/scripts/perfect_hash_gen.mjs canonical_headers.phf.json --out canonical_headers_phf.go

// LookupCanonicalHeader identifies a known ingress header by name. It accepts
// Go's canonical form (net/http keys such as "Content-Type") and the lowercase
// wire form (HTTP/2). The table is generated from canonical_headers.phf.json.
func LookupCanonicalHeader(key string) (CanonicalHeaderID, bool) {
	return lookupCanonicalHeader(key)
}
