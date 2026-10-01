package oauthx

import (
	"context"
	"time"
)

// IdentityProvider defines the interface for an OAuth 2.0 / OIDC provider.
type IdentityProvider interface {
	// Name returns the provider identifier.
	Name() string

	// AuthURL generates the authorization URL with PKCE verifier and challenge.
	AuthURL(state string, opts ...AuthOption) (authURL string, verifier string, err error)

	// Exchange trades an authorization code and verifier for a TokenSet.
	Exchange(ctx context.Context, code, verifier string) (*TokenSet, error)

	// Refresh exchanges a refresh token for an updated TokenSet.
	Refresh(ctx context.Context, refreshToken string) (*TokenSet, error)

	// Revoke invalidates an access or refresh token.
	Revoke(ctx context.Context, token string) error

	// UserInfo fetches profile claims from the provider OIDC endpoint.
	UserInfo(ctx context.Context, accessToken string) (*UserProfile, error)

	// ValidateIDToken validates and extracts claims from an OIDC ID token.
	ValidateIDToken(ctx context.Context, idToken string) (*Claims, error)
}

// TokenSet contains the tokens returned by code exchange or refresh.
type TokenSet struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
	Scopes       []string  `json:"scopes,omitempty"`
}

// Claims contains verified standard OpenID Connect claims.
type Claims struct {
	Subject       string    `json:"sub"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"email_verified"`
	Name          string    `json:"name"`
	Picture       string    `json:"picture,omitempty"`
	Issuer        string    `json:"iss"`
	Audience      string    `json:"aud"`
	IssuedAt      time.Time `json:"iat"`
	ExpiresAt     time.Time `json:"exp"`
}

// UserProfile contains claims returned by the OIDC userinfo endpoint.
type UserProfile struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name,omitempty"`
	FamilyName    string `json:"family_name,omitempty"`
	Picture       string `json:"picture,omitempty"`
	Locale        string `json:"locale,omitempty"`
}

// AuthOption configures authorization request options.
type AuthOption func(*authOptions)

type authOptions struct {
	accessType string
	prompt     string
	loginHint  string
	scopes     []string
}

// WithOfflineAccess requests a refresh token.
func WithOfflineAccess() AuthOption {
	return func(o *authOptions) {
		o.accessType = "offline"
	}
}

// WithPrompt forces the consent prompt.
func WithPrompt(prompt string) AuthOption {
	return func(o *authOptions) {
		o.prompt = prompt
	}
}

// WithLoginHint pre-fills the email address in the consent screen.
func WithLoginHint(hint string) AuthOption {
	return func(o *authOptions) {
		o.loginHint = hint
	}
}

// WithScopes appends additional OAuth scopes.
func WithScopes(scopes ...string) AuthOption {
	return func(o *authOptions) {
		o.scopes = append(o.scopes, scopes...)
	}
}
