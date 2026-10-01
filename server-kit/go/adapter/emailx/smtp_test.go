package emailx

import (
	"testing"
)

func TestParseQueueRef(t *testing.T) {
	tests := []struct {
		reply    string
		expected string
	}{
		{
			reply:    "250 2.0.0 Message queued with id 10.",
			expected: "16", // 0x10 == 16
		},
		{
			reply:    "250 2.0.0 Message queued with id 1a2b.",
			expected: "6699", // 0x1a2b == 6699
		},
		{
			reply:    "250 2.0.0 Ok: queued as 4X8yZ9",
			expected: "4X8yZ9",
		},
		{
			reply:    "250 Ok: message accepted",
			expected: "",
		},
	}

	for _, tc := range tests {
		got := ParseQueueRef(tc.reply)
		if got != tc.expected {
			t.Errorf("reply %q: expected %q, got %q", tc.reply, tc.expected, got)
		}
	}
}

func TestSMTPConfigValidation(t *testing.T) {
	_, err := NewSMTPSender(SMTPConfig{})
	if err == nil {
		t.Fatal("expected error with empty SMTP host")
	}

	sender, err := NewSMTPSender(SMTPConfig{
		Host: "smtp.example.com",
	})
	if err != nil || sender == nil {
		t.Fatalf("failed to create SMTP sender: %v", err)
	}
	if sender.config.Port != 587 {
		t.Fatalf("expected default port 587, got %d", sender.config.Port)
	}
}
