package whatsappx

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ActionScope binds an action to the application-selected conversation and actor.
// Version changes invalidate prior actions; the application must also atomically
// consume the action to prevent replay within its expiry window.
type ActionScope struct{ TenantID, Recipient, ConversationID, Version string }

func validScope(s ActionScope) bool {
	return boundedText(s.TenantID, 256, true) && boundedText(s.Recipient, 64, true) && boundedText(s.ConversationID, 256, true) && boundedText(s.Version, 64, true)
}

// SignActionToken creates a compact conversation-bound token for quick replies.
func SignActionToken(secret []byte, scope ActionScope, action string, expires time.Time) (string, error) {
	if len(secret) < 32 || !validScope(scope) || !validPathID(action) || len(action) > 64 || expires.Unix() <= 0 {
		return "", errors.New("whatsappx: invalid action binding")
	}
	payload := action + "." + strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(accountKey(scope.TenantID, scope.Recipient, scope.ConversationID, scope.Version, payload)))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// VerifyActionToken checks binding and expiry; it does not authorize or consume an action.
func VerifyActionToken(secret []byte, scope ActionScope, token string, now time.Time) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("whatsappx: invalid action token")
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || expiry <= now.Unix() {
		return "", errors.New("whatsappx: expired action token")
	}
	expected, err := SignActionToken(secret, scope, parts[0], time.Unix(expiry, 0))
	if err != nil || !hmac.Equal([]byte(expected), []byte(token)) {
		return "", errors.New("whatsappx: invalid action token")
	}
	return parts[0], nil
}
