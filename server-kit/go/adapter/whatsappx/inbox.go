package whatsappx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// WebhookAccount maps a configured subscription to its owning tenant.
type WebhookAccount struct{ BusinessAccountID, PhoneNumberID, TenantID string }

// WebhookConfig is validated before an ingress handler can be installed.
type WebhookConfig struct {
	VerifyToken, AppSecret string
	Accounts               []WebhookAccount
	AcceptanceTimeout      time.Duration
}

// WebhookRecord is an independently deduplicated event, including unsupported
// message types which applications must persist and quarantine rather than execute.
type WebhookRecord struct {
	TenantID, BusinessAccountID, PhoneNumberID, Key, Kind string
	Message                                               *InboundMessage
	Status                                                *MessageStatus
}

// WebhookDelivery contains verified evidence bounded by the 1 MiB ingress limit.
type WebhookDelivery struct {
	Event   WebhookEvent
	RawBody []byte
	Records []WebhookRecord
}

// WebhookInbox owns durable acceptance. Accept MUST commit all records and pending
// jobs in one transaction before returning nil, with a unique (tenant,key) constraint.
// Existing duplicates return nil. Failure/uncertain commit returns an error; retries
// use the same keys. Implementations must honor cancellation and retain raw evidence
// under their privacy/retention policy. An in-memory implementation is not durable.
type WebhookInbox interface {
	Accept(context.Context, WebhookDelivery) error
}
type durableIngress struct {
	accounts map[string]WebhookAccount
	inbox    WebhookInbox
	timeout  time.Duration
}

// NewDurableWebhookHandler adds persistence-aware acknowledgment without changing
// the legacy observer callback API. Production ingress must migrate to this constructor.
func NewDurableWebhookHandler(cfg WebhookConfig, inbox WebhookInbox) (*WebhookHandler, error) {
	if !boundedText(cfg.VerifyToken, 512, true) || !boundedText(cfg.AppSecret, 512, true) || inbox == nil || len(cfg.Accounts) == 0 || len(cfg.Accounts) > 1000 {
		return nil, errors.New("whatsappx: invalid ingress configuration")
	}
	if cfg.AcceptanceTimeout == 0 {
		cfg.AcceptanceTimeout = 5 * time.Second
	}
	if cfg.AcceptanceTimeout < 0 || cfg.AcceptanceTimeout > 30*time.Second {
		return nil, errors.New("whatsappx: invalid acceptance timeout")
	}
	d := &durableIngress{accounts: map[string]WebhookAccount{}, inbox: inbox, timeout: cfg.AcceptanceTimeout}
	for _, a := range cfg.Accounts {
		if !boundedText(a.TenantID, 256, true) || !boundedText(a.BusinessAccountID, 256, true) || !boundedText(a.PhoneNumberID, 256, true) {
			return nil, errors.New("whatsappx: invalid account mapping")
		}
		key := accountKey(a.BusinessAccountID, a.PhoneNumberID)
		if _, exists := d.accounts[key]; exists {
			return nil, errors.New("whatsappx: duplicate account mapping")
		}
		d.accounts[key] = a
	}
	h := NewWebhookHandler(cfg.VerifyToken, cfg.AppSecret, nil)
	h.durable = d
	return h, nil
}
func accountKey(parts ...string) string {
	b, _ := json.Marshal(parts)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (d *durableIngress) accept(ctx context.Context, event WebhookEvent, body []byte) error {
	if event.Object != "whatsapp_business_account" || len(event.Entry) == 0 {
		return errors.New("whatsappx: unsupported subscription")
	}
	delivery := WebhookDelivery{Event: event, RawBody: append([]byte(nil), body...)}
	for _, entry := range event.Entry {
		for _, change := range entry.Changes {
			value := change.Value
			a, ok := d.accounts[accountKey(entry.ID, value.Metadata.PhoneNumberID)]
			if !ok || change.Field != "messages" || value.MessagingProduct != "whatsapp" {
				return errors.New("whatsappx: unconfigured subscription")
			}
			for i := range value.Messages {
				m := &value.Messages[i]
				if !boundedText(m.ID, 256, true) || !boundedText(m.From, 64, true) || !boundedText(m.Type, 64, true) {
					return errors.New("whatsappx: invalid message identity")
				}
				delivery.Records = append(delivery.Records, WebhookRecord{TenantID: a.TenantID, BusinessAccountID: entry.ID, PhoneNumberID: a.PhoneNumberID, Kind: "message", Key: accountKey(entry.ID, a.PhoneNumberID, "message", m.ID), Message: m})
			}
			for i := range value.Statuses {
				s := &value.Statuses[i]
				if !boundedText(s.ID, 256, true) || !boundedText(s.Status, 64, true) || !boundedText(s.Timestamp, 64, true) {
					return errors.New("whatsappx: invalid receipt identity")
				}
				delivery.Records = append(delivery.Records, WebhookRecord{TenantID: a.TenantID, BusinessAccountID: entry.ID, PhoneNumberID: a.PhoneNumberID, Kind: "status", Key: accountKey(entry.ID, a.PhoneNumberID, "status", s.ID, s.Status, s.Timestamp), Status: s})
			}
			if len(delivery.Records) > 1000 {
				return errors.New("whatsappx: event count exceeded")
			}
		}
	}
	if len(delivery.Records) == 0 {
		return errors.New("whatsappx: empty delivery")
	}
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	if err := d.inbox.Accept(ctx, delivery); err != nil {
		return err
	}
	return ctx.Err()
}
