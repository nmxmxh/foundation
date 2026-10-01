package oauthx

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGoogleConfigValidation(t *testing.T) {
	c := GoogleConfig{}
	if c.Valid() {
		t.Fatal("empty config should be invalid")
	}

	c = GoogleConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://example.com/callback",
	}
	if !c.Valid() {
		t.Fatal("valid config rejected")
	}

	g, err := NewGoogle(c)
	if err != nil || g == nil {
		t.Fatalf("failed to create Google provider: %v", err)
	}

	_, err = NewGoogle(GoogleConfig{})
	if err == nil {
		t.Fatal("expected error with empty config")
	}
}

func TestPKCEAndAuthURL(t *testing.T) {
	g, _ := NewGoogle(GoogleConfig{
		ClientID:     "client-123",
		ClientSecret: "secret-456",
		RedirectURL:  "https://app.example.com/oauth/google/callback",
	})

	if _, _, err := g.AuthURL(""); err == nil {
		t.Fatal("expected error with empty state")
	}

	authURL, verifier, err := g.AuthURL("random-state-123",
		WithOfflineAccess(),
		WithPrompt("consent"),
		WithLoginHint("user@example.com"),
		WithScopes("https://www.googleapis.com/auth/calendar.readonly"),
	)
	if err != nil {
		t.Fatalf("failed to generate auth URL: %v", err)
	}

	if verifier == "" || len(verifier) < 40 {
		t.Fatalf("invalid verifier length: %d", len(verifier))
	}

	h := sha256.Sum256([]byte(verifier))
	expectedChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("failed to parse auth URL: %v", err)
	}

	q := u.Query()
	if q.Get("client_id") != "client-123" {
		t.Fatalf("unexpected client_id: %s", q.Get("client_id"))
	}
	if q.Get("code_challenge") != expectedChallenge {
		t.Fatalf("expected challenge %s, got %s", expectedChallenge, q.Get("code_challenge"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Fatalf("unexpected method: %s", q.Get("code_challenge_method"))
	}
	if q.Get("access_type") != "offline" {
		t.Fatalf("unexpected access_type: %s", q.Get("access_type"))
	}
	if q.Get("prompt") != "consent" {
		t.Fatalf("unexpected prompt: %s", q.Get("prompt"))
	}
	if q.Get("login_hint") != "user@example.com" {
		t.Fatalf("unexpected login_hint: %s", q.Get("login_hint"))
	}
	if !strings.Contains(q.Get("scope"), "calendar.readonly") {
		t.Fatalf("scope missing custom scope: %s", q.Get("scope"))
	}
}

func TestExchangeAndRefresh(t *testing.T) {
	g, _ := NewGoogle(GoogleConfig{
		ClientID:     "client-123",
		ClientSecret: "secret-456",
		RedirectURL:  "https://app.example.com/oauth/google/callback",
	})

	g.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		vals, _ := url.ParseQuery(string(body))

		if vals.Get("grant_type") == "authorization_code" {
			if vals.Get("code") != "auth-code-1" || vals.Get("code_verifier") != "verifier-1" {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`)),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"access_token": "acc-123",
					"refresh_token": "ref-456",
					"id_token": "id-tok",
					"token_type": "Bearer",
					"expires_in": 3600,
					"scope": "openid email"
				}`)),
			}, nil
		}

		if vals.Get("grant_type") == "refresh_token" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"access_token": "acc-new",
					"token_type": "Bearer",
					"expires_in": 3600
				}`)),
			}, nil
		}

		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})

	// Exchange test
	toks, err := g.Exchange(context.Background(), "auth-code-1", "verifier-1")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if toks.AccessToken != "acc-123" || toks.RefreshToken != "ref-456" {
		t.Fatalf("unexpected tokens: %+v", toks)
	}

	// Empty exchange test
	if _, err := g.Exchange(context.Background(), "", ""); err == nil {
		t.Fatal("expected error with empty code")
	}

	// Refresh test
	refToks, err := g.Refresh(context.Background(), "ref-456")
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if refToks.AccessToken != "acc-new" || refToks.RefreshToken != "ref-456" {
		t.Fatalf("unexpected refreshed tokens: %+v", refToks)
	}

	if _, err := g.Refresh(context.Background(), ""); err == nil {
		t.Fatal("expected error with empty refresh token")
	}
}

func TestRevokeAndUserInfo(t *testing.T) {
	g, _ := NewGoogle(GoogleConfig{
		ClientID:     "client-123",
		ClientSecret: "secret-456",
		RedirectURL:  "https://app.example.com/oauth/google/callback",
	})

	g.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "revoke") {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		}
		if strings.Contains(r.URL.Path, "userinfo") {
			if r.Header.Get("Authorization") != "Bearer valid-token" {
				return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`{
					"sub": "user-sub-1",
					"email": "test@example.com",
					"email_verified": true,
					"name": "Jane Doe",
					"given_name": "Jane",
					"family_name": "Doe",
					"locale": "en"
				}`)),
			}, nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})

	if err := g.Revoke(context.Background(), "some-token"); err != nil {
		t.Fatalf("revoke failed: %v", err)
	}
	if err := g.Revoke(context.Background(), ""); err == nil {
		t.Fatal("expected error with empty token")
	}

	profile, err := g.UserInfo(context.Background(), "valid-token")
	if err != nil {
		t.Fatalf("userinfo failed: %v", err)
	}
	if profile.Email != "test@example.com" || profile.Name != "Jane Doe" {
		t.Fatalf("unexpected profile: %+v", profile)
	}

	if _, err := g.UserInfo(context.Background(), ""); err == nil {
		t.Fatal("expected error with empty token")
	}

	// Error branch tests
	gErr, _ := NewGoogle(GoogleConfig{
		ClientID: "client-123", ClientSecret: "secret-456", RedirectURL: "https://example.com",
	})
	gErr.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"error":"bad"}`))}, nil
	})
	if err := gErr.Revoke(context.Background(), "tok"); err == nil {
		t.Fatal("expected error for bad revoke status")
	}
	if _, err := gErr.UserInfo(context.Background(), "tok"); err == nil {
		t.Fatal("expected error for bad userinfo status")
	}
	if _, err := gErr.Exchange(context.Background(), "code", "ver"); err == nil {
		t.Fatal("expected error for bad exchange status")
	}
}

func TestValidateIDToken(t *testing.T) {
	g, _ := NewGoogle(GoogleConfig{
		ClientID:     "client-123",
		ClientSecret: "secret-456",
		RedirectURL:  "https://app.example.com/oauth/google/callback",
	})

	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":            "https://accounts.google.com",
		"aud":            "client-123",
		"sub":            "sub-999",
		"email":          "alice@example.com",
		"email_verified": true,
		"name":           "Alice Smith",
		"iat":            now.Unix(),
		"exp":            now.Add(time.Hour).Unix(),
	})
	validIDToken, _ := token.SignedString([]byte("mock-secret"))

	claims, err := g.ValidateIDToken(context.Background(), validIDToken)
	if err != nil {
		t.Fatalf("failed to validate ID token: %v", err)
	}
	if claims.Email != "alice@example.com" || claims.Subject != "sub-999" {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	expiredToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "https://accounts.google.com",
		"aud": "client-123",
		"exp": now.Add(-time.Hour).Unix(),
	})
	expiredIDToken, _ := expiredToken.SignedString([]byte("mock-secret"))
	if _, err := g.ValidateIDToken(context.Background(), expiredIDToken); err == nil {
		t.Fatal("expected error for expired token")
	}

	audToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "https://accounts.google.com",
		"aud": "wrong-client",
		"exp": now.Add(time.Hour).Unix(),
	})
	audIDToken, _ := audToken.SignedString([]byte("mock-secret"))
	if _, err := g.ValidateIDToken(context.Background(), audIDToken); err == nil {
		t.Fatal("expected error for audience mismatch")
	}

	issToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "https://evil.example.com",
		"aud": "client-123",
		"exp": now.Add(time.Hour).Unix(),
	})
	issIDToken, _ := issToken.SignedString([]byte("mock-secret"))
	if _, err := g.ValidateIDToken(context.Background(), issIDToken); err == nil {
		t.Fatal("expected error for issuer mismatch")
	}

	if _, err := g.ValidateIDToken(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty token")
	}
	if _, err := g.ValidateIDToken(context.Background(), "invalid-jwt"); err == nil {
		t.Fatal("expected error for malformed token")
	}
}
