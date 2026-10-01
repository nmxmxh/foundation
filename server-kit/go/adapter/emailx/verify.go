package emailx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"strings"
)

// ValidateAddress checks whether an email address conforms to RFC 5321 / 5322.
func ValidateAddress(raw string) error {
	if len(raw) == 0 {
		return errors.New("emailx: empty email address")
	}
	if len(raw) > 254 {
		return errors.New("emailx: email address exceeds 254 octets limit")
	}

	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return fmt.Errorf("emailx: invalid address format: %w", err)
	}

	parts := strings.Split(addr.Address, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("emailx: address must contain local-part and domain")
	}

	if len(parts[0]) > 64 {
		return errors.New("emailx: local-part exceeds 64 octets limit")
	}

	if !strings.Contains(parts[1], ".") {
		return errors.New("emailx: domain must contain at least one dot")
	}

	return nil
}

// CheckDomainMX queries DNS for Mail Exchanger (MX) records for a domain.
func CheckDomainMX(ctx context.Context, domain string) ([]string, error) {
	cleanDomain := strings.TrimSpace(strings.ToLower(domain))
	if cleanDomain == "" {
		return nil, errors.New("emailx: domain cannot be empty")
	}

	resolver := net.DefaultResolver
	mxRecords, err := resolver.LookupMX(ctx, cleanDomain)
	if err != nil {
		return nil, fmt.Errorf("emailx: mx lookup for %q failed: %w", cleanDomain, err)
	}

	if len(mxRecords) == 0 {
		return nil, fmt.Errorf("emailx: no mx records found for %q", cleanDomain)
	}

	hosts := make([]string, 0, len(mxRecords))
	for _, mx := range mxRecords {
		hosts = append(hosts, strings.TrimRight(mx.Host, "."))
	}
	return hosts, nil
}

// CheckOneClickCompliance verifies that RFC 8058 one-click unsubscribe headers are properly configured.
func CheckOneClickCompliance(msg Message) bool {
	if msg.ListUnsubscribe == nil {
		return false
	}
	return msg.ListUnsubscribe.URL != "" && msg.ListUnsubscribe.OneClick
}
