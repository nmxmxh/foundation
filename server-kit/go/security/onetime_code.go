package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ErrOneTimeCodeDigits means the requested code length is out of range.
var ErrOneTimeCodeDigits = errors.New("security: one-time code digits must be between 4 and 10")

// OneTimeCode generates and checks short numeric codes (SMS or email OTP).
//
// Codes are never stored. Store Hash(scope, code) and compare with Verify.
// The scope binds a code to one challenge (for example, "profile:phone"), so a
// code issued for one number cannot confirm another. The key derives from a
// deployment secret and a purpose label, so two features that share the
// secret still produce unrelated hashes.
//
// Attempt limits, expiry and send rate limits are persistence concerns and
// belong to the caller. A wrong guess must be counted in a transaction that
// commits even when the check fails.
type OneTimeCode struct {
	key []byte
}

// NewOneTimeCode derives a purpose-scoped HMAC key from a deployment secret.
func NewOneTimeCode(secret, purpose string) OneTimeCode {
	sum := sha256.Sum256([]byte("ovasabi:onetime:" + purpose + "\x00" + secret))
	return OneTimeCode{key: sum[:]}
}

// Generate returns a uniformly random numeric code with the given digits.
func (o OneTimeCode) Generate(digits int) (string, error) {
	if digits < 4 || digits > 10 {
		return "", ErrOneTimeCodeDigits
	}
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*s", digits, n.String()), nil
}

// Hash returns the storable HMAC of a code within a scope.
func (o OneTimeCode) Hash(scope, code string) string {
	mac := hmac.New(sha256.New, o.key)
	mac.Write([]byte(scope))
	mac.Write([]byte{0})
	mac.Write([]byte(strings.TrimSpace(code)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify compares a submitted code with a stored hash in constant time.
func (o OneTimeCode) Verify(scope, code, storedHash string) bool {
	if strings.TrimSpace(code) == "" || storedHash == "" {
		return false
	}
	return hmac.Equal([]byte(o.Hash(scope, code)), []byte(storedHash))
}
