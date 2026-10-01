package emailx

import (
	"strings"
	"testing"
)

func TestBuildRFC5322(t *testing.T) {
	// 1. Single-part text message
	msg1 := Message{
		From:     "sender@example.com",
		To:       []string{"recipient@example.com"},
		Subject:  "Simple subject",
		TextBody: "Hello from test",
	}

	bytes1, msgID1, err := BuildRFC5322(msg1)
	if err != nil {
		t.Fatalf("failed to build text message: %v", err)
	}
	s1 := string(bytes1)
	if !strings.Contains(s1, "From: sender@example.com") {
		t.Fatal("missing From header")
	}
	if !strings.Contains(s1, "Message-ID: "+msgID1) {
		t.Fatal("missing Message-ID header")
	}
	if !strings.Contains(s1, "Hello from test") {
		t.Fatal("missing text body")
	}

	// 2. Alternative text + HTML message with RFC 8058 One-Click Unsubscribe
	msg2 := Message{
		From:     "Marketing <newsletter@example.com>",
		To:       []string{"Alice <alice@example.com>"},
		Subject:  "Monthly Updates",
		TextBody: "Plain text version",
		HTMLBody: "<h1>HTML version</h1>",
		ListUnsubscribe: &ListUnsubscribe{
			URL:      "https://example.com/unsub?id=123",
			Email:    "unsub@example.com",
			OneClick: true,
		},
	}

	bytes2, _, err := BuildRFC5322(msg2)
	if err != nil {
		t.Fatalf("failed to build alternative message: %v", err)
	}
	s2 := string(bytes2)
	if !strings.Contains(s2, "multipart/alternative") {
		t.Fatal("expected multipart/alternative")
	}
	if !strings.Contains(s2, "List-Unsubscribe: <https://example.com/unsub?id=123>, <mailto:unsub@example.com>") {
		t.Fatal("missing List-Unsubscribe header")
	}
	if !strings.Contains(s2, "List-Unsubscribe-Post: List-Unsubscribe=One-Click") {
		t.Fatal("missing List-Unsubscribe-Post header")
	}

	// 3. Message with Attachments (multipart/mixed)
	msg3 := Message{
		From:     "billing@example.com",
		To:       []string{"customer@example.com"},
		Subject:  "Your Invoice",
		TextBody: "Please find attached your invoice.",
		Attachments: []Attachment{
			{
				Filename:    "invoice.pdf",
				ContentType: "application/pdf",
				Data:        []byte("fake-pdf-content"),
			},
		},
	}

	bytes3, _, err := BuildRFC5322(msg3)
	if err != nil {
		t.Fatalf("failed to build attachment message: %v", err)
	}
	s3 := string(bytes3)
	if !strings.Contains(s3, "multipart/mixed") {
		t.Fatal("expected multipart/mixed")
	}
	if !strings.Contains(s3, "filename=\"invoice.pdf\"") {
		t.Fatal("missing attachment filename")
	}

	// 4. Non-ASCII header encoding (RFC 2047)
	msg4 := Message{
		From:     "René <rene@example.com>",
		To:       []string{"Müller <muller@example.com>"},
		Subject:  "Café & Croissant 🥐",
		TextBody: "Bon appétit",
	}

	bytes4, _, err := BuildRFC5322(msg4)
	if err != nil {
		t.Fatalf("failed to build RFC 2047 message: %v", err)
	}
	s4 := string(bytes4)
	if !strings.Contains(s4, "=?UTF-8?B?") {
		t.Fatal("expected RFC 2047 encoded non-ASCII subject")
	}

	// 5. Attachment filename sanitization (path traversal & CRLF injection)
	msg5 := Message{
		From:     "security@example.com",
		To:       []string{"admin@example.com"},
		Subject:  "Security alert",
		TextBody: "Alert",
		Attachments: []Attachment{
			{
				Filename:    "../../evil\r\nX-Injected: true.pdf",
				ContentType: "application/pdf",
				Data:        []byte("%PDF-test"),
			},
		},
	}
	bytes5, _, err := BuildRFC5322(msg5)
	if err != nil {
		t.Fatalf("failed to build sanitized message: %v", err)
	}
	s5 := string(bytes5)
	if strings.Contains(s5, "..") || strings.Contains(s5, "\r\nX-Injected") {
		t.Fatal("CRLF or path traversal not stripped from attachment filename")
	}
	if !strings.Contains(s5, "evilX-Injected: true.pdf") {
		t.Fatalf("expected sanitized filename, got: %s", s5)
	}
}
