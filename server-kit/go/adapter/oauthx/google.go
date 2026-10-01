package oauthx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/metadata"
)

const (
	defaultGoogleAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleTokenURL    = "https://oauth2.googleapis.com/token"
	defaultGoogleRevokeURL   = "https://oauth2.googleapis.com/revoke"
	defaultGoogleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	maxResponseLimit         = 64 * 1024 // 64 KiB
)

// GoogleConfig contains credentials for Google OAuth 2.0.
type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string

	// Overrides for unit testing
	AuthURL     string
	TokenURL    string
	RevokeURL   string
	UserInfoURL string
}

func (c GoogleConfig) Valid() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
}

// Google implements the IdentityProvider interface for Google services.
type Google struct {
	config GoogleConfig
	client *http.Client
}

// NewGoogle creates an initialized Google identity provider.
func NewGoogle(config GoogleConfig) (*Google, error) {
	if !config.Valid() {
		return nil, errors.New("oauthx: ClientID, ClientSecret, and RedirectURL are required")
	}
	if len(config.Scopes) == 0 {
		config.Scopes = []string{"openid", "email", "profile"}
	}
	if config.AuthURL == "" {
		config.AuthURL = defaultGoogleAuthURL
	}
	if config.TokenURL == "" {
		config.TokenURL = defaultGoogleTokenURL
	}
	if config.RevokeURL == "" {
		config.RevokeURL = defaultGoogleRevokeURL
	}
	if config.UserInfoURL == "" {
		config.UserInfoURL = defaultGoogleUserInfoURL
	}

	return &Google{
		config: config,
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (g *Google) Name() string {
	return "google"
}

// generatePKCE generates a secure random verifier and its S256 challenge.
func generatePKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge, nil
}

// AuthURL constructs the Google authorization redirect URL with PKCE.
func (g *Google) AuthURL(state string, opts ...AuthOption) (string, string, error) {
	if state == "" {
		return "", "", errors.New("oauthx: state parameter cannot be empty")
	}

	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", "", err
	}

	options := &authOptions{}
	for _, opt := range opts {
		opt(options)
	}

	scopes := g.config.Scopes
	if len(options.scopes) > 0 {
		scopes = append(scopes, options.scopes...)
	}

	vals := url.Values{
		"client_id":             {g.config.ClientID},
		"redirect_uri":          {g.config.RedirectURL},
		"response_type":         {"code"},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}

	if options.accessType != "" {
		vals.Set("access_type", options.accessType)
	}
	if options.prompt != "" {
		vals.Set("prompt", options.prompt)
	}
	if options.loginHint != "" {
		vals.Set("login_hint", options.loginHint)
	}

	authURL := fmt.Sprintf("%s?%s", g.config.AuthURL, vals.Encode())
	return authURL, verifier, nil
}

type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// Exchange trades an authorization code and PKCE verifier for tokens.
func (g *Google) Exchange(ctx context.Context, code, verifier string) (*TokenSet, error) {
	if code == "" || verifier == "" {
		return nil, errors.New("oauthx: code and verifier are required")
	}

	data := url.Values{
		"code":          {code},
		"client_id":     {g.config.ClientID},
		"client_secret": {g.config.ClientSecret},
		"redirect_uri":  {g.config.RedirectURL},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}

	return g.postToken(ctx, data)
}

// Refresh fetches new tokens using a refresh token.
func (g *Google) Refresh(ctx context.Context, refreshToken string) (*TokenSet, error) {
	if refreshToken == "" {
		return nil, errors.New("oauthx: refresh token cannot be empty")
	}

	data := url.Values{
		"refresh_token": {refreshToken},
		"client_id":     {g.config.ClientID},
		"client_secret": {g.config.ClientSecret},
		"grant_type":    {"refresh_token"},
	}

	tok, err := g.postToken(ctx, data)
	if err != nil {
		return nil, err
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refreshToken
	}
	return tok, nil
}

func (g *Google) postToken(ctx context.Context, form url.Values) (*TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	g.applyCorrelation(ctx, req)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauthx: token request failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseLimit))
	if err != nil {
		return nil, fmt.Errorf("oauthx: read token response: %w", err)
	}

	var res googleTokenResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("oauthx: parse token response: %w", err)
	}

	if resp.StatusCode >= 400 || res.Error != "" {
		return nil, fmt.Errorf("oauthx: token exchange error: %s", res.Error)
	}

	expiresAt := time.Now().Add(time.Duration(res.ExpiresIn) * time.Second)
	var scopes []string
	if res.Scope != "" {
		scopes = strings.Split(res.Scope, " ")
	}

	return &TokenSet{
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		IDToken:      res.IDToken,
		TokenType:    res.TokenType,
		ExpiresAt:    expiresAt,
		Scopes:       scopes,
	}, nil
}

// Revoke invalidates an access or refresh token.
func (g *Google) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return errors.New("oauthx: token to revoke cannot be empty")
	}

	data := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.config.RevokeURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	g.applyCorrelation(ctx, req)

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("oauthx: revoke request failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("oauthx: revoke returned status %d", resp.StatusCode)
	}
	return nil
}

// UserInfo fetches profile claims from Google's OpenID Connect endpoint.
func (g *Google) UserInfo(ctx context.Context, accessToken string) (*UserProfile, error) {
	if accessToken == "" {
		return nil, errors.New("oauthx: access token cannot be empty")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.config.UserInfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	g.applyCorrelation(ctx, req)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauthx: userinfo request failed: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseLimit))
	if err != nil {
		return nil, fmt.Errorf("oauthx: read userinfo: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("oauthx: userinfo returned status %d", resp.StatusCode)
	}

	var profile UserProfile
	if err := json.Unmarshal(body, &profile); err != nil {
		return nil, fmt.Errorf("oauthx: decode userinfo: %w", err)
	}
	return &profile, nil
}

// ValidateIDToken validates and extracts claims from an ID token JWT.
func (g *Google) ValidateIDToken(_ context.Context, idToken string) (*Claims, error) {
	if idToken == "" {
		return nil, errors.New("oauthx: ID token cannot be empty")
	}

	// Parse unverified claims structure first
	token, _, err := jwt.NewParser().ParseUnverified(idToken, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("oauthx: invalid ID token format: %w", err)
	}

	claimsMap, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("oauthx: invalid token claims")
	}

	// Verify Issuer
	iss, _ := claimsMap["iss"].(string)
	if iss != "https://accounts.google.com" && iss != "accounts.google.com" {
		return nil, fmt.Errorf("oauthx: invalid token issuer %q", iss)
	}

	// Verify Audience
	aud, _ := claimsMap["aud"].(string)
	if aud != g.config.ClientID {
		return nil, errors.New("oauthx: token audience mismatch")
	}

	// Verify Expiration
	expNum, ok := claimsMap["exp"].(float64)
	if !ok || time.Now().Unix() > int64(expNum) {
		return nil, errors.New("oauthx: token has expired")
	}

	iatNum, _ := claimsMap["iat"].(float64)
	sub, _ := claimsMap["sub"].(string)
	email, _ := claimsMap["email"].(string)
	emailVerified, _ := claimsMap["email_verified"].(bool)
	name, _ := claimsMap["name"].(string)
	picture, _ := claimsMap["picture"].(string)

	return &Claims{
		Subject:       sub,
		Email:         email,
		EmailVerified: emailVerified,
		Name:          name,
		Picture:       picture,
		Issuer:        iss,
		Audience:      aud,
		IssuedAt:      time.Unix(int64(iatNum), 0),
		ExpiresAt:     time.Unix(int64(expNum), 0),
	}, nil
}

func (g *Google) applyCorrelation(ctx context.Context, req *http.Request) {
	corr := metadata.FromContext(ctx).CorrelationID
	if corr != "" {
		req.Header.Set("X-Correlation-ID", corr)
	}
}
