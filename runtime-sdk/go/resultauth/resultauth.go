// Package resultauth authenticates the origin of a compute receipt.
//
// # What this proves, and what it does not
//
// A tag from this package proves that whoever produced a receipt held the
// session key. That is authentication. It is not a proof that the computation
// was performed, or performed correctly.
//
// The distinction is the entire reason this package is named for what it does.
// A peer holding a valid session key can sign an arbitrary wrong answer and the
// tag will verify perfectly. So can a peer whose kernel is a version behind,
// whose GPU is thermally throttling into wrong results, or which is simply
// buggy. None of that is detectable here, at any key length.
//
// The post-mortem of the deleted swarm branch records a function named
// generateFastProof, labelled "HMAC-SHA256-FAST", which computed FNV-1a and was
// verified by checking the string's length. Two separate failures, and the
// naming is the worse one: a system designed around a value called "proof"
// inherits a guarantee nobody built. This package would rather be a smaller
// thing that is true.
//
// # The integrity ladder
//
// Rung 1, implemented here: authentication. The receipt came from a holder of
// the session key and was not altered in transit.
//
// Rung 2, NOT implemented: redundant execution and agreement. Dispatch to N
// peers, accept on agreement. Costs N times the compute and is the cheapest
// real defence against a peer that lies.
//
// Rung 3, NOT implemented: probabilistic spot-checking. Re-execute a random
// fraction locally. Against an adversary that lies more than once, the detection
// probability compounds; against a single opportunistic lie it does nothing.
//
// Rung 4, NOT implemented: deterministic replay. Re-run the unit and compare.
// This has a hard prerequisite that constrains the whole design above it —
// floating-point reductions are not bit-reproducible across heterogeneous
// hardware, because parallel reduction reassociates, FMA contraction differs,
// and transcendental implementations disagree between WebGPU, WASM SIMD and
// native. Integer accumulation is associative, so INT8 into INT32 replays
// exactly and float32 does not. Verifiable mesh compute must therefore be
// integer or fixed-point, and RuntimeComputeCapsule already carries the
// computeFlagDeterministic bit that would mark it.
//
// Until rungs 2 through 4 exist, a receipt verified here is a receipt from an
// authenticated source, and dispatching compute to peers outside the trust
// boundary has no integrity guarantee at all. Callers must not read Verify as
// more than it is.
package resultauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

// TagBytes is the length of an authentication tag: SHA-256's output size.
const TagBytes = sha256.Size

// MinKeyBytes is the shortest session key accepted.
//
// HMAC tolerates any key length — it hashes anything longer than the block size
// and zero-pads anything shorter — so nothing here would fail on a two-byte key
// except the security. Thirty-two bytes is the output width of the hash and the
// point past which the key stops being the weakest part.
const MinKeyBytes = 32

// domainPrefix separates this package's tags from any other use of the same
// session key. Without it, a key shared with another protocol would let a
// message from that protocol be replayed as a receipt, or the reverse.
const domainPrefix = "ovrt.resultauth.v1"

var (
	// ErrKeyTooShort is returned when a session key is below MinKeyBytes.
	ErrKeyTooShort = errors.New("resultauth: session key is shorter than the minimum")
	// ErrTagLength is returned when a tag is not TagBytes long.
	ErrTagLength = errors.New("resultauth: tag is not the expected length")
)

// Key is a session-scoped HMAC key.
//
// Ephemeral by construction: it is derived per connection and lives only as long
// as that connection. A key that outlives its session turns a single compromised
// peer into a permanent forger.
type Key struct {
	material []byte
}

// NewKey wraps session key material.
//
// The material is copied, so the caller may zero its own buffer afterwards.
func NewKey(material []byte) (Key, error) {
	if len(material) < MinKeyBytes {
		return Key{}, fmt.Errorf("%w: got %d bytes, want at least %d",
			ErrKeyTooShort, len(material), MinKeyBytes)
	}
	owned := make([]byte, len(material))
	copy(owned, material)
	return Key{material: owned}, nil
}

// Receipt is the set of fields an authentication tag covers.
//
// It mirrors the authenticable subset of RuntimeComputeReceipt from
// runtime-sdk/protocols/system/v1/runtime_compute.capnp. The result itself is
// covered by hash rather than by value, so a large result does not have to be
// held in memory to be authenticated — which is also why ResultHash must be a
// hash the verifier computed itself, never one the producer supplied.
type Receipt struct {
	JobID           string
	TenantID        string
	UnitID          string
	WorkerID        string
	StatusCode      uint32
	ResultHash      string
	StartedUnixMS   uint64
	CompletedUnixMS uint64
}

// Authenticate returns the tag for a receipt.
func (k Key) Authenticate(receipt Receipt) ([]byte, error) {
	if len(k.material) < MinKeyBytes {
		return nil, ErrKeyTooShort
	}
	mac := hmac.New(sha256.New, k.material)
	// hash.Hash documents Write as never returning an error, so the canonical
	// encoder below cannot fail; it takes the writer anyway so the encoding is
	// testable in isolation.
	writeCanonical(mac, receipt)
	return mac.Sum(nil), nil
}

// AuthenticateHex returns the tag as a lowercase hex string, for transports that
// carry it as text.
func (k Key) AuthenticateHex(receipt Receipt) (string, error) {
	tag, err := k.Authenticate(receipt)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(tag), nil
}

// Verify reports whether tag authenticates receipt under this key.
//
// The comparison is constant-time. A byte-by-byte comparison that returns early
// leaks, through timing, how much of a guessed tag was correct, which turns
// forging a tag from a 2^256 problem into a 32-step one.
//
// Returns false rather than an error for every failure, including a malformed
// tag: a caller that must branch on *why* authentication failed will eventually
// leak that reason to whoever supplied the tag.
func (k Key) Verify(receipt Receipt, tag []byte) bool {
	if len(tag) != TagBytes {
		return false
	}
	expected, err := k.Authenticate(receipt)
	if err != nil {
		return false
	}
	return hmac.Equal(expected, tag)
}

// VerifyHex reports whether a hex-encoded tag authenticates receipt.
func (k Key) VerifyHex(receipt Receipt, tag string) bool {
	raw, err := hex.DecodeString(tag)
	if err != nil {
		return false
	}
	return k.Verify(receipt, raw)
}

// writeCanonical serialises a receipt unambiguously.
//
// Every variable-length field is length-prefixed. Concatenating them raw would
// make the encoding ambiguous — ("ab", "c") and ("a", "bc") produce identical
// bytes — which lets anyone who can influence two adjacent fields move a byte
// across the boundary between them and keep the tag valid. A job id and a tenant
// id are exactly such a pair, so this is a cross-tenant hole, not a theoretical
// one. TestCanonicalEncodingHasNoAmbiguousFieldBoundaries holds the line.
//
// Fixed-width fields need no prefix because their width is the delimiter.
func writeCanonical(w interface{ Write([]byte) (int, error) }, receipt Receipt) {
	writeLengthPrefixed(w, []byte(domainPrefix))
	writeLengthPrefixed(w, []byte(receipt.JobID))
	writeLengthPrefixed(w, []byte(receipt.TenantID))
	writeLengthPrefixed(w, []byte(receipt.UnitID))
	writeLengthPrefixed(w, []byte(receipt.WorkerID))
	writeLengthPrefixed(w, []byte(receipt.ResultHash))

	var fixed [20]byte
	binary.BigEndian.PutUint32(fixed[0:4], receipt.StatusCode)
	binary.BigEndian.PutUint64(fixed[4:12], receipt.StartedUnixMS)
	binary.BigEndian.PutUint64(fixed[12:20], receipt.CompletedUnixMS)
	_, _ = w.Write(fixed[:])
}

func writeLengthPrefixed(w interface{ Write([]byte) (int, error) }, field []byte) {
	var prefix [8]byte
	binary.BigEndian.PutUint64(prefix[:], uint64(len(field)))
	_, _ = w.Write(prefix[:])
	_, _ = w.Write(field)
}
