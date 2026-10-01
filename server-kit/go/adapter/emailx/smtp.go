package emailx

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/adapter"
)

const defaultSMTPTimeout = 15 * time.Second

// SMTPConfig holds connection settings for SMTP transmission.
type SMTPConfig struct {
	Host      string
	Port      int
	Username  string
	Password  string
	DirectTLS bool // Port 465
	Timeout   time.Duration
}

// SMTPSender delivers email through standard SMTP / STARTTLS.
type SMTPSender struct {
	config SMTPConfig
}

// NewSMTPSender creates a configured SMTP email sender.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if cfg.Host == "" {
		return nil, errors.New("emailx: SMTP host is required")
	}
	if cfg.Port <= 0 {
		cfg.Port = 587
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultSMTPTimeout
	}
	return &SMTPSender{config: cfg}, nil
}

// Send delivers one email message over SMTP.
func (s *SMTPSender) Send(ctx context.Context, msg Message) (*DeliveryReceipt, error) {
	rawBytes, msgID, err := BuildRFC5322(msg)
	if err != nil {
		return nil, fmt.Errorf("emailx: build rfc5322: %w", err)
	}

	if msg.DKIM != nil {
		signed, dkimErr := SignDKIM(rawBytes, *msg.DKIM)
		if dkimErr != nil {
			return nil, fmt.Errorf("emailx: dkim sign: %w", dkimErr)
		}
		rawBytes = signed
	}

	allRecipients := append([]string{}, msg.To...)
	allRecipients = append(allRecipients, msg.Cc...)
	allRecipients = append(allRecipients, msg.Bcc...)

	if len(allRecipients) == 0 {
		return nil, errors.New("emailx: recipient list cannot be empty")
	}

	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	dialer := &net.Dialer{Timeout: s.config.Timeout}

	var (
		client *smtp.Client
		conn   net.Conn
	)

	if s.config.DirectTLS {
		tlsConn, dialErr := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: s.config.Host})
		if dialErr != nil {
			return nil, fmt.Errorf("smtp tls dial: %w", dialErr)
		}
		conn = tlsConn
		c, clErr := smtp.NewClient(tlsConn, s.config.Host)
		if clErr != nil {
			_ = tlsConn.Close()
			return nil, fmt.Errorf("smtp tls client: %w", clErr)
		}
		client = c
	} else {
		tcpConn, dialErr := dialer.DialContext(ctx, "tcp", addr)
		if dialErr != nil {
			return nil, fmt.Errorf("smtp dial: %w", dialErr)
		}
		conn = tcpConn
		c, clErr := smtp.NewClient(tcpConn, s.config.Host)
		if clErr != nil {
			_ = tcpConn.Close()
			return nil, fmt.Errorf("smtp client: %w", clErr)
		}
		client = c

		if hasStartTLS, _ := client.Extension("STARTTLS"); hasStartTLS {
			if tlsErr := client.StartTLS(&tls.Config{ServerName: s.config.Host}); tlsErr != nil {
				_ = client.Close()
				return nil, fmt.Errorf("smtp starttls: %w", tlsErr)
			}
		}
	}
	defer func() {
		if client != nil {
			_ = client.Close()
		}
		if conn != nil {
			_ = conn.Close()
		}
	}()

	if s.config.Username != "" {
		auth := smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.Host)
		if authErr := client.Auth(auth); authErr != nil {
			return nil, fmt.Errorf("smtp auth: %w", authErr)
		}
	}

	if mailErr := client.Mail(msg.From); mailErr != nil {
		return nil, fmt.Errorf("smtp mail from: %w", mailErr)
	}

	for _, rcpt := range allRecipients {
		if rcptErr := client.Rcpt(rcpt); rcptErr != nil {
			return nil, fmt.Errorf("smtp rcpt to %s: %w", rcpt, rcptErr)
		}
	}

	w, dataErr := client.Data()
	if dataErr != nil {
		return nil, fmt.Errorf("smtp data: %w", dataErr)
	}
	if _, writeErr := w.Write(rawBytes); writeErr != nil {
		_ = w.Close()
		return nil, fmt.Errorf("smtp write body: %w", writeErr)
	}
	if closeErr := w.Close(); closeErr != nil {
		return nil, fmt.Errorf("smtp close data: %w", closeErr)
	}

	code, reply, readErr := client.Text.ReadResponse(250)
	if readErr != nil {
		return nil, fmt.Errorf("smtp final reply (%d): %w", code, readErr)
	}

	queueRef := ParseQueueRef(fmt.Sprintf("%d %s", code, reply))

	return &DeliveryReceipt{
		MessageID:  msgID,
		QueueID:    queueRef,
		Provider:   "smtp",
		StatusCode: code,
		SentAt:     time.Now().UTC(),
	}, nil
}

// ParseQueueRef extracts queue IDs from Stalwart hex responses or standard ESMTP replies.
func ParseQueueRef(reply string) string {
	// Stalwart pattern: `queued with id <hex>`
	const marker = "queued with id "
	if idx := strings.Index(reply, marker); idx >= 0 {
		raw := strings.Trim(strings.TrimSpace(reply[idx+len(marker):]), ".")
		if n, err := strconv.ParseUint(raw, 16, 64); err == nil {
			return strconv.FormatUint(n, 10)
		}
		return raw
	}

	// Standard ESMTP: `queued as <id>`
	const queuedAs = "queued as "
	if idx := strings.Index(reply, queuedAs); idx >= 0 {
		fields := strings.Fields(reply[idx+len(queuedAs):])
		if len(fields) > 0 {
			return strings.Trim(fields[0], ".")
		}
	}

	return ""
}

// Probe evaluates connection health with the SMTP server.
func (s *SMTPSender) Probe(ctx context.Context) (adapter.Health, error) {
	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	dialer := &net.Dialer{Timeout: 5 * time.Second}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return adapter.HealthNotServing, err
	}
	_ = conn.Close()

	return adapter.HealthServing, nil
}

func (s *SMTPSender) Close() error {
	return nil
}
