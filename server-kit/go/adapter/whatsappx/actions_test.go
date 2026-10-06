package whatsappx

import (
	"testing"
	"time"
)

func TestActionBinding(t *testing.T) {
	secret := []byte("01234567890123456789012345678901")
	scope := ActionScope{"tenant", "2348012345678", "conversation", "1"}
	now := time.Unix(100, 0)
	token, err := SignActionToken(secret, scope, "accept", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if action, err := VerifyActionToken(secret, scope, token, now); err != nil || action != "accept" {
		t.Fatal(action, err)
	}
	other := scope
	other.ConversationID = "different"
	for _, s := range []ActionScope{other, {}} {
		if _, err := VerifyActionToken(secret, s, token, now); err == nil {
			t.Fatal("scope bypass")
		}
	}
	for _, tok := range []string{"bad", token + "x", "accept.invalid.signature"} {
		if _, err := VerifyActionToken(secret, scope, tok, now); err == nil {
			t.Fatal("invalid accepted")
		}
	}
	if _, err := VerifyActionToken(secret, scope, token, now.Add(time.Minute)); err == nil {
		t.Fatal("expired accepted")
	}
	if _, err := SignActionToken(nil, scope, "accept", now); err == nil {
		t.Fatal("weak secret accepted")
	}
}
