package resultauth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func testKey(t *testing.T) Key {
	t.Helper()
	key, err := NewKey(bytes.Repeat([]byte{0x5a}, MinKeyBytes))
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return key
}

func sampleReceipt() Receipt {
	return Receipt{
		JobID:           "job-7f3a",
		TenantID:        "org-42",
		UnitID:          "pronto.fusion.v2",
		WorkerID:        "worker-3",
		StatusCode:      0,
		ResultHash:      "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		StartedUnixMS:   1_786_732_080_000,
		CompletedUnixMS: 1_786_732_080_412,
	}
}

// ---------------------------------------------------------------------------
// The naming claim: this is HMAC-SHA256, and here is the proof
// ---------------------------------------------------------------------------

// TestPrimitiveIsActuallyHMACSHA256 is the direct answer to issue 1 of the
// swarm-experiment post-mortem, in which a function announcing "HMAC-SHA256-FAST"
// computed FNV-1a. A name is a claim, and a claim about a cryptographic
// primitive should be checkable against the standard's own test vector.
//
// RFC 4231, Test Case 1.
func TestPrimitiveIsActuallyHMACSHA256(t *testing.T) {
	key := bytes.Repeat([]byte{0x0b}, 20)
	data := []byte("Hi There")
	const want = "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"

	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := hex.EncodeToString(mac.Sum(nil))

	if got != want {
		t.Fatalf("the primitive this package uses is not HMAC-SHA256:\n got %s\nwant %s", got, want)
	}
}

func TestTagIsSHA256Width(t *testing.T) {
	// A short tag is a truncated tag, and truncation is a security parameter
	// that must never change by accident.
	tag, err := testKey(t).Authenticate(sampleReceipt())
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if len(tag) != TagBytes {
		t.Fatalf("tag is %d bytes, want %d", len(tag), TagBytes)
	}
	if TagBytes != 32 {
		t.Fatalf("TagBytes is %d; SHA-256 produces 32", TagBytes)
	}
}

// ---------------------------------------------------------------------------
// Round trip and tamper detection
// ---------------------------------------------------------------------------

func TestVerifyAcceptsAnUnalteredReceipt(t *testing.T) {
	key := testKey(t)
	receipt := sampleReceipt()

	tag, err := key.Authenticate(receipt)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if !key.Verify(receipt, tag) {
		t.Fatal("a freshly authenticated receipt did not verify")
	}
}

func TestVerifyRejectsEveryAlteredField(t *testing.T) {
	// Table-driven over every field, because a field left out of the canonical
	// encoding is a field an attacker can change freely while the tag stays
	// valid — and the failure is invisible until someone tries it.
	key := testKey(t)
	original := sampleReceipt()

	tag, err := key.Authenticate(original)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	cases := []struct {
		field string
		alter func(*Receipt)
	}{
		{"JobID", func(r *Receipt) { r.JobID = "job-0000" }},
		{"TenantID", func(r *Receipt) { r.TenantID = "org-43" }},
		{"UnitID", func(r *Receipt) { r.UnitID = "pronto.fusion.v3" }},
		{"WorkerID", func(r *Receipt) { r.WorkerID = "worker-4" }},
		{"StatusCode", func(r *Receipt) { r.StatusCode = 1 }},
		{"ResultHash", func(r *Receipt) { r.ResultHash = strings.Repeat("0", 64) }},
		{"StartedUnixMS", func(r *Receipt) { r.StartedUnixMS++ }},
		{"CompletedUnixMS", func(r *Receipt) { r.CompletedUnixMS++ }},
	}

	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			altered := original
			tc.alter(&altered)
			if key.Verify(altered, tag) {
				t.Errorf("%s was altered and the tag still verified; the field is not covered", tc.field)
			}
		})
	}
}

func TestVerifyRejectsAForeignKey(t *testing.T) {
	receipt := sampleReceipt()

	mine := testKey(t)
	theirs, err := NewKey(bytes.Repeat([]byte{0xa5}, MinKeyBytes))
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	tag, err := theirs.Authenticate(receipt)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if mine.Verify(receipt, tag) {
		t.Fatal("a receipt signed with a different session key verified")
	}
}

func TestVerifyRejectsMalformedTags(t *testing.T) {
	key := testKey(t)
	receipt := sampleReceipt()

	good, err := key.Authenticate(receipt)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if key.Verify(receipt, nil) {
		t.Error("a nil tag verified")
	}
	if key.Verify(receipt, []byte{}) {
		t.Error("an empty tag verified")
	}
	if key.Verify(receipt, good[:TagBytes-1]) {
		t.Error("a truncated tag verified")
	}
	if key.Verify(receipt, append(append([]byte{}, good...), 0x00)) {
		t.Error("an over-long tag verified")
	}

	flipped := append([]byte{}, good...)
	flipped[0] ^= 0x01
	if key.Verify(receipt, flipped) {
		t.Error("a tag with one flipped bit verified")
	}
}

// ---------------------------------------------------------------------------
// Canonicalisation
// ---------------------------------------------------------------------------

// TestCanonicalEncodingHasNoAmbiguousFieldBoundaries is the test that justifies
// length-prefixing. Raw concatenation would make these two receipts serialise
// identically, so anyone able to influence a job id could shift a byte into the
// tenant id and keep the tag valid — a cross-tenant forgery from a formatting
// decision.
func TestCanonicalEncodingHasNoAmbiguousFieldBoundaries(t *testing.T) {
	key := testKey(t)

	left := Receipt{JobID: "ab", TenantID: "c"}
	right := Receipt{JobID: "a", TenantID: "bc"}

	leftTag, err := key.AuthenticateHex(left)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	rightTag, err := key.AuthenticateHex(right)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if leftTag == rightTag {
		t.Fatal(`("ab","c") and ("a","bc") produced the same tag; the encoding is ambiguous`)
	}
	if key.VerifyHex(right, leftTag) {
		t.Fatal("a tag for one field split verified against another")
	}
}

func TestEmptyFieldsAreDistinguishedFromAbsentOnes(t *testing.T) {
	// An empty string and a missing field must not collide either; the length
	// prefix is what separates them.
	key := testKey(t)

	withEmpty := Receipt{JobID: "job", TenantID: ""}
	withValue := Receipt{JobID: "job", TenantID: "x"}

	emptyTag, err := key.AuthenticateHex(withEmpty)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	valueTag, err := key.AuthenticateHex(withValue)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if emptyTag == valueTag {
		t.Fatal("an empty field and a populated one produced the same tag")
	}
}

func TestAuthenticationIsDeterministic(t *testing.T) {
	// The verifier recomputes rather than storing, so a tag that varied between
	// calls would fail verification against itself.
	key := testKey(t)
	receipt := sampleReceipt()

	first, err := key.AuthenticateHex(receipt)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	for i := range 8 {
		again, err := key.AuthenticateHex(receipt)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if again != first {
			t.Fatalf("call %d produced a different tag: %s != %s", i, again, first)
		}
	}
}

func TestDomainPrefixSeparatesThisUseOfTheKey(t *testing.T) {
	// A key shared with another protocol must not let that protocol's messages
	// replay as receipts. The prefix is what prevents it, so a bare HMAC over
	// the same bytes must not match.
	key := testKey(t)
	receipt := sampleReceipt()

	tag, err := key.Authenticate(receipt)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	var undomained bytes.Buffer
	writeLengthPrefixed(&undomained, []byte(receipt.JobID))
	mac := hmac.New(sha256.New, bytes.Repeat([]byte{0x5a}, MinKeyBytes))
	if _, err := mac.Write(undomained.Bytes()); err != nil {
		t.Fatalf("write: %v", err)
	}

	if hmac.Equal(tag, mac.Sum(nil)) {
		t.Fatal("the tag matched an undomained HMAC over the same key")
	}
	if !strings.HasPrefix(domainPrefix, "ovrt.") {
		t.Fatalf("domain prefix %q does not identify this runtime", domainPrefix)
	}
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func TestShortKeysAreRejected(t *testing.T) {
	// HMAC accepts any key length by zero-padding, so nothing fails on a weak
	// key except the security. The floor has to be enforced here or not at all.
	for _, size := range []int{0, 1, 8, 16, MinKeyBytes - 1} {
		if _, err := NewKey(bytes.Repeat([]byte{0x01}, size)); !errors.Is(err, ErrKeyTooShort) {
			t.Errorf("a %d byte key was accepted (err=%v)", size, err)
		}
	}
	if _, err := NewKey(bytes.Repeat([]byte{0x01}, MinKeyBytes)); err != nil {
		t.Errorf("a %d byte key was rejected: %v", MinKeyBytes, err)
	}
}

func TestTheZeroKeyCannotAuthenticate(t *testing.T) {
	// Key's zero value is reachable by declaration, and must not silently
	// produce tags under an empty key.
	var unset Key

	if _, err := unset.Authenticate(sampleReceipt()); !errors.Is(err, ErrKeyTooShort) {
		t.Errorf("the zero Key produced a tag (err=%v)", err)
	}
	if unset.Verify(sampleReceipt(), make([]byte, TagBytes)) {
		t.Error("the zero Key verified a tag")
	}
}

func TestKeyMaterialIsCopiedFromTheCaller(t *testing.T) {
	// Callers are expected to zero their own buffers after handing over session
	// material; a key that aliased that buffer would be silently zeroed with it.
	material := bytes.Repeat([]byte{0x11}, MinKeyBytes)
	key, err := NewKey(material)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}

	before, err := key.AuthenticateHex(sampleReceipt())
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	for i := range material {
		material[i] = 0
	}

	after, err := key.AuthenticateHex(sampleReceipt())
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if before != after {
		t.Fatal("zeroing the caller's buffer changed the key; the material was aliased, not copied")
	}
}

func TestVerifyHexRejectsNonHex(t *testing.T) {
	key := testKey(t)
	receipt := sampleReceipt()

	for _, tag := range []string{"", "zz", "not-a-tag", strings.Repeat("g", 64)} {
		if key.VerifyHex(receipt, tag) {
			t.Errorf("non-hex tag %q verified", tag)
		}
	}
}

func TestHexRoundTrip(t *testing.T) {
	key := testKey(t)
	receipt := sampleReceipt()

	tag, err := key.AuthenticateHex(receipt)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if len(tag) != TagBytes*2 {
		t.Fatalf("hex tag is %d chars, want %d", len(tag), TagBytes*2)
	}
	if !key.VerifyHex(receipt, tag) {
		t.Fatal("a hex tag did not verify")
	}
	if !key.VerifyHex(receipt, strings.ToUpper(tag)) {
		t.Fatal("an upper-case hex tag did not verify; hex decoding should be case-insensitive")
	}
}
