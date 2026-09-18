package security

import (
	"errors"
	"regexp"
	"testing"
)

func TestOneTimeCodeGenerateShape(t *testing.T) {
	otp := NewOneTimeCode("secret", "phone")
	digitsOnly := regexp.MustCompile(`^\d{6}$`)
	seen := map[string]struct{}{}
	for range 200 {
		code, err := otp.Generate(6)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if !digitsOnly.MatchString(code) {
			t.Fatalf("code %q is not six digits", code)
		}
		seen[code] = struct{}{}
	}
	if len(seen) < 150 {
		t.Fatalf("only %d distinct codes in 200 draws; generator looks biased", len(seen))
	}
	for _, bad := range []int{3, 11} {
		if _, err := otp.Generate(bad); !errors.Is(err, ErrOneTimeCodeDigits) {
			t.Fatalf("Generate(%d): got %v, want ErrOneTimeCodeDigits", bad, err)
		}
	}
}

func TestOneTimeCodeVerifyBindsScopeAndPurpose(t *testing.T) {
	phone := NewOneTimeCode("secret", "phone")
	stored := phone.Hash("profile-1:+2348030000000", "123456")

	if !phone.Verify("profile-1:+2348030000000", " 123456 ", stored) {
		t.Fatal("the right code in the right scope was refused")
	}
	if phone.Verify("profile-1:+2348030000000", "123457", stored) {
		t.Fatal("a wrong code was accepted")
	}
	if phone.Verify("profile-1:+2348039999999", "123456", stored) {
		t.Fatal("a code issued for one number confirmed another")
	}
	if NewOneTimeCode("secret", "email").Verify("profile-1:+2348030000000", "123456", stored) {
		t.Fatal("a different purpose produced the same hash")
	}
	if NewOneTimeCode("other-secret", "phone").Verify("profile-1:+2348030000000", "123456", stored) {
		t.Fatal("a different secret produced the same hash")
	}
	if phone.Verify("scope", "", stored) || phone.Verify("scope", "123456", "") {
		t.Fatal("empty code or empty stored hash must never verify")
	}
}
