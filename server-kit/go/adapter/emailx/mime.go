package emailx

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/mail"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BuildRFC5322 converts a Message struct into RFC 5322 compliant MIME bytes.
func BuildRFC5322(msg Message) ([]byte, string, error) {
	b := &bytes.Buffer{}

	fromDomain := "localhost"
	if addr, err := mail.ParseAddress(msg.From); err == nil {
		parts := strings.Split(addr.Address, "@")
		if len(parts) == 2 {
			fromDomain = parts[1]
		}
	}

	msgID := msg.MessageID
	if msgID == "" {
		msgID = fmt.Sprintf("<%s@%s>", uuid.NewString(), fromDomain)
	}

	dateStr := time.Now().UTC().Format(time.RFC1123Z)

	// Required headers
	writeHeader(b, "From", encodeHeader(msg.From))
	writeHeader(b, "To", strings.Join(encodeAddressList(msg.To), ", "))
	if len(msg.Cc) > 0 {
		writeHeader(b, "Cc", strings.Join(encodeAddressList(msg.Cc), ", "))
	}
	if msg.ReplyTo != "" {
		writeHeader(b, "Reply-To", encodeHeader(msg.ReplyTo))
	}
	writeHeader(b, "Subject", encodeHeader(msg.Subject))
	writeHeader(b, "Date", dateStr)
	writeHeader(b, "Message-ID", msgID)
	writeHeader(b, "MIME-Version", "1.0")

	// Custom headers
	for k, v := range msg.Headers {
		writeHeader(b, k, v)
	}

	// RFC 8058 One-Click Unsubscribe
	if msg.ListUnsubscribe != nil {
		var parts []string
		if msg.ListUnsubscribe.URL != "" {
			parts = append(parts, fmt.Sprintf("<%s>", msg.ListUnsubscribe.URL))
		}
		if msg.ListUnsubscribe.Email != "" {
			parts = append(parts, fmt.Sprintf("<mailto:%s>", msg.ListUnsubscribe.Email))
		}
		if len(parts) > 0 {
			writeHeader(b, "List-Unsubscribe", strings.Join(parts, ", "))
			if msg.ListUnsubscribe.OneClick {
				writeHeader(b, "List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
			}
		}
	}

	hasAttachments := len(msg.Attachments) > 0
	hasBothBodies := msg.TextBody != "" && msg.HTMLBody != ""

	switch {
	case hasAttachments:
		mixedBoundary := randomBoundary("mixed")
		writeHeader(b, "Content-Type", fmt.Sprintf("multipart/mixed; boundary=\"%s\"", mixedBoundary))
		b.WriteString("\r\n")

		// Write body part
		b.WriteString(fmt.Sprintf("--%s\r\n", mixedBoundary))
		if hasBothBodies {
			altBoundary := randomBoundary("alt")
			b.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", altBoundary))
			writeAlternativeBodies(b, altBoundary, msg.TextBody, msg.HTMLBody)
		} else if msg.HTMLBody != "" {
			writeHTMLPart(b, msg.HTMLBody)
		} else {
			writeTextPart(b, msg.TextBody)
		}

		// Write attachments
		for _, att := range msg.Attachments {
			b.WriteString(fmt.Sprintf("\r\n--%s\r\n", mixedBoundary))
			writeAttachmentPart(b, att)
		}
		b.WriteString(fmt.Sprintf("\r\n--%s--\r\n", mixedBoundary))

	case hasBothBodies:
		altBoundary := randomBoundary("alt")
		writeHeader(b, "Content-Type", fmt.Sprintf("multipart/alternative; boundary=\"%s\"", altBoundary))
		b.WriteString("\r\n")
		writeAlternativeBodies(b, altBoundary, msg.TextBody, msg.HTMLBody)

	case msg.HTMLBody != "":
		writeHeader(b, "Content-Type", "text/html; charset=UTF-8")
		writeHeader(b, "Content-Transfer-Encoding", "base64")
		b.WriteString("\r\n")
		writeBase64Wrapped(b, []byte(msg.HTMLBody))

	default:
		writeHeader(b, "Content-Type", "text/plain; charset=UTF-8")
		writeHeader(b, "Content-Transfer-Encoding", "8bit")
		b.WriteString("\r\n")
		b.WriteString(msg.TextBody)
		b.WriteString("\r\n")
	}

	return b.Bytes(), msgID, nil
}

func writeHeader(b *bytes.Buffer, key, val string) {
	b.WriteString(fmt.Sprintf("%s: %s\r\n", key, val))
}

func writeAlternativeBodies(b *bytes.Buffer, boundary, text, html string) {
	b.WriteString(fmt.Sprintf("--%s\r\n", boundary))
	writeTextPart(b, text)
	b.WriteString(fmt.Sprintf("\r\n--%s\r\n", boundary))
	writeHTMLPart(b, html)
	b.WriteString(fmt.Sprintf("\r\n--%s--\r\n", boundary))
}

func writeTextPart(b *bytes.Buffer, text string) {
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(text)
	b.WriteString("\r\n")
}

func writeHTMLPart(b *bytes.Buffer, html string) {
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	writeBase64Wrapped(b, []byte(html))
}

func writeAttachmentPart(b *bytes.Buffer, att Attachment) {
	contentType := att.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	safeFilename := sanitizeAttachmentFilename(att.Filename)
	b.WriteString(fmt.Sprintf("Content-Type: %s; name=\"%s\"\r\n", contentType, safeFilename))
	b.WriteString("Content-Transfer-Encoding: base64\r\n")

	if att.ContentID != "" {
		b.WriteString(fmt.Sprintf("Content-Disposition: inline; filename=\"%s\"\r\n", safeFilename))
		b.WriteString(fmt.Sprintf("Content-ID: <%s>\r\n\r\n", att.ContentID))
	} else {
		b.WriteString(fmt.Sprintf("Content-Disposition: attachment; filename=\"%s\"\r\n\r\n", safeFilename))
	}

	writeBase64Wrapped(b, att.Data)
}

func sanitizeAttachmentFilename(name string) string {
	clean := filepath.Base(filepath.Clean(name))
	clean = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\r' || r == '\n' || r == '\\' {
			return -1
		}
		return r
	}, clean)
	if clean == "" || clean == "." {
		return "attachment.bin"
	}
	return clean
}

func writeBase64Wrapped(b *bytes.Buffer, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	const lineLength = 76
	for len(encoded) > lineLength {
		b.WriteString(encoded[:lineLength])
		b.WriteString("\r\n")
		encoded = encoded[lineLength:]
	}
	if len(encoded) > 0 {
		b.WriteString(encoded)
		b.WriteString("\r\n")
	}
}

func randomBoundary(prefix string) string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("=_ovasabi_%s_%x", prefix, buf)
}

func encodeAddressList(addrs []string) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, encodeHeader(a))
	}
	return out
}

func encodeHeader(s string) string {
	for _, r := range s {
		if r > 127 {
			return fmt.Sprintf("=?UTF-8?B?%s?=", base64.StdEncoding.EncodeToString([]byte(s)))
		}
	}
	return s
}
