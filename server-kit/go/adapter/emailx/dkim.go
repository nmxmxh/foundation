package emailx

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SignDKIM signs an RFC 5322 message payload using DKIM1 relaxed/relaxed RSA-SHA256.
func SignDKIM(msgBytes []byte, cfg DKIMConfig) ([]byte, error) {
	if cfg.Domain == "" || cfg.Selector == "" || cfg.PrivateKey == "" {
		return nil, errors.New("emailx: DKIM domain, selector, and private key are required")
	}

	block, _ := pem.Decode([]byte(cfg.PrivateKey))
	if block == nil {
		return nil, errors.New("emailx: failed to decode DKIM private key PEM")
	}

	var rsaKey *rsa.PrivateKey
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		rsaKey = key
	} else if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if k, ok := key.(*rsa.PrivateKey); ok {
			rsaKey = k
		}
	}

	if rsaKey == nil {
		return nil, errors.New("emailx: unsupported DKIM private key format (RSA required)")
	}

	headerBytes, bodyBytes := splitHeaderAndBody(msgBytes)

	// Canonicalize body: relaxed
	canBody := canonicalizeBodyRelaxed(bodyBytes)
	bodyHash := sha256.Sum256([]byte(canBody))
	bh := base64.StdEncoding.EncodeToString(bodyHash[:])

	// Headers to sign
	headersToSign := []string{"from", "to", "subject", "date", "message-id", "mime-version", "content-type"}
	var foundHeaders []string
	for _, h := range headersToSign {
		if headerExists(headerBytes, h) {
			foundHeaders = append(foundHeaders, h)
		}
	}
	if headerExists(headerBytes, "list-unsubscribe") {
		foundHeaders = append(foundHeaders, "list-unsubscribe")
	}
	if headerExists(headerBytes, "list-unsubscribe-post") {
		foundHeaders = append(foundHeaders, "list-unsubscribe-post")
	}

	hTag := strings.Join(foundHeaders, ":")
	tTag := fmt.Sprintf("%d", time.Now().Unix())

	dkimParams := fmt.Sprintf("v=1; a=rsa-sha256; c=relaxed/relaxed; d=%s; s=%s; t=%s; h=%s; bh=%s; b=",
		cfg.Domain, cfg.Selector, tTag, hTag, bh)

	// Canonicalize selected headers
	canHeaders := canonicalizeHeadersRelaxed(headerBytes, foundHeaders)
	canDKIMHeader := fmt.Sprintf("dkim-signature:%s", dkimParams)
	signingData := canHeaders + canDKIMHeader

	h := sha256.Sum256([]byte(signingData))
	sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, h[:])
	if err != nil {
		return nil, fmt.Errorf("emailx: rsa dkim sign: %w", err)
	}

	bSig := base64.StdEncoding.EncodeToString(sig)
	finalDKIMHeader := fmt.Sprintf("DKIM-Signature: %s%s\r\n", dkimParams, bSig)

	// Prepend DKIM-Signature to raw message
	return append([]byte(finalDKIMHeader), msgBytes...), nil
}

// FormatDKIMRecord splits a public key into RFC 1035 chunked strings (<= 255 chars).
func FormatDKIMRecord(pubKeyPEM string) (string, error) {
	block, _ := pem.Decode([]byte(pubKeyPEM))
	if block == nil {
		return "", errors.New("emailx: failed to decode public key PEM")
	}

	b64Key := base64.StdEncoding.EncodeToString(block.Bytes)
	fullVal := fmt.Sprintf("v=DKIM1; k=rsa; p=%s", b64Key)

	// RFC 1035 chunking: max 255 bytes per chunk
	const maxChunk = 250
	var chunks []string
	for len(fullVal) > maxChunk {
		chunks = append(chunks, fmt.Sprintf("\"%s\"", fullVal[:maxChunk]))
		fullVal = fullVal[maxChunk:]
	}
	if len(fullVal) > 0 {
		chunks = append(chunks, fmt.Sprintf("\"%s\"", fullVal))
	}

	return strings.Join(chunks, " "), nil
}

func splitHeaderAndBody(msg []byte) (string, string) {
	s := string(msg)
	idx := strings.Index(s, "\r\n\r\n")
	if idx < 0 {
		idx = strings.Index(s, "\n\n")
		if idx < 0 {
			return s, ""
		}
		return s[:idx], s[idx+2:]
	}
	return s[:idx], s[idx+4:]
}

func headerExists(headers string, name string) bool {
	lines := strings.Split(headers, "\n")
	target := strings.ToLower(name) + ":"
	for _, l := range lines {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), target) {
			return true
		}
	}
	return false
}

var multiSpace = regexp.MustCompile(`[ \t]+`)

func canonicalizeBodyRelaxed(body string) string {
	lines := strings.Split(body, "\n")
	var out []string
	for _, l := range lines {
		trimmed := strings.TrimRight(l, "\r\n")
		trimmed = strings.TrimRight(trimmed, " \t")
		trimmed = multiSpace.ReplaceAllString(trimmed, " ")
		out = append(out, trimmed)
	}

	// Trim trailing empty lines
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\r\n") + "\r\n"
}

func canonicalizeHeadersRelaxed(rawHeaders string, names []string) string {
	var b strings.Builder
	for _, name := range names {
		val := findHeaderValue(rawHeaders, name)
		if val != "" {
			nameLower := strings.ToLower(name)
			cleanVal := multiSpace.ReplaceAllString(strings.TrimSpace(val), " ")
			b.WriteString(fmt.Sprintf("%s:%s\r\n", nameLower, cleanVal))
		}
	}
	return b.String()
}

func findHeaderValue(raw, name string) string {
	lines := strings.Split(raw, "\n")
	target := strings.ToLower(name) + ":"
	for i, l := range lines {
		clean := strings.TrimRight(l, "\r\n")
		if strings.HasPrefix(strings.ToLower(clean), target) {
			val := clean[len(target):]
			// Check continuation lines
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimRight(lines[j], "\r\n")
				if strings.HasPrefix(next, " ") || strings.HasPrefix(next, "\t") {
					val += " " + strings.TrimSpace(next)
				} else {
					break
				}
			}
			return val
		}
	}
	return ""
}
