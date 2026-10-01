package emailx

import (
	"strings"
	"testing"
)

func TestValidateAddress(t *testing.T) {
	valid := []string{
		"user@example.com",
		"first.last@sub.domain.co",
		"customer+tag@company.org",
	}

	for _, addr := range valid {
		if err := ValidateAddress(addr); err != nil {
			t.Errorf("address %q should be valid, got: %v", addr, err)
		}
	}

	invalid := []string{
		"",
		"plainaddress",
		"@missinglocal.com",
		"missingdomain@",
		"nodots@domain",
		strings.Repeat("a", 65) + "@domain.com",
		strings.Repeat("a", 255) + "@domain.com",
	}

	for _, addr := range invalid {
		if err := ValidateAddress(addr); err == nil {
			t.Errorf("address %q should be invalid", addr)
		}
	}
}

func TestCheckOneClickCompliance(t *testing.T) {
	msgNonCompliant := Message{}
	if CheckOneClickCompliance(msgNonCompliant) {
		t.Fatal("empty message should not be one-click compliant")
	}

	msgURLOnly := Message{
		ListUnsubscribe: &ListUnsubscribe{
			URL:      "https://example.com/unsub",
			OneClick: false,
		},
	}
	if CheckOneClickCompliance(msgURLOnly) {
		t.Fatal("message without OneClick flag should not be one-click compliant")
	}

	msgCompliant := Message{
		ListUnsubscribe: &ListUnsubscribe{
			URL:      "https://example.com/unsub",
			OneClick: true,
		},
	}
	if !CheckOneClickCompliance(msgCompliant) {
		t.Fatal("message with URL and OneClick=true must be compliant")
	}
}
