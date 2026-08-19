package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	errInvalidTokenFormat  = errors.New("invalid token format")
	errInvalidSignature    = errors.New("invalid token signature")
	errTokenExpired        = errors.New("token expired")
	errTokenIssuedInFuture = errors.New("token issued in the future")
	errInvalidTokenType    = errors.New("invalid token type")
)

const (
	TokenTypeAccess  = "access"
	TokenTypeRefresh = "refresh"

	// ClockSkewLeeway tolerates clock drift between the issuer and validator
	// (e.g. across pods in a multi-instance deployment). Without it, a freshly
	// minted token can be rejected as expired by a validator whose clock runs
	// slightly ahead — surfacing to clients as a spurious unauthorized/logout.
	//
	// It is applied symmetrically, to `exp` in one direction and `iat` in the
	// other. Skew is not directional: the same drift that makes a valid token
	// look expired on one pod makes it look future-dated on another, and
	// tolerating only the first leaves the second failing for the same reason.
	// RFC 8725 §3.9 puts the ceiling on how generous this may be — leeway
	// lengthens the window a leaked token stays usable, so it stays small.
	ClockSkewLeeway = 60 * time.Second
)

// Claims captures the security context transported by tokens.
type Claims struct {
	UserID         string   `json:"user_id"`
	Email          string   `json:"email,omitempty"`
	Role           string   `json:"role,omitempty"`
	OrganizationID string   `json:"organization_id,omitempty"`
	DeviceID       string   `json:"device_id,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
	TokenType      string   `json:"token_type,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	IssuedAt       int64    `json:"iat"`
	ExpiresAt      int64    `json:"exp"`
}

// JWTManager manages token issuance and validation.
type JWTManager struct {
	secret []byte
}

// NewJWTManager creates a token manager with HMAC signing.
func NewJWTManager(secret string) (*JWTManager, error) {
	if len(strings.TrimSpace(secret)) < 16 {
		return nil, errors.New("jwt secret must be at least 16 characters")
	}
	return &JWTManager{secret: []byte(secret)}, nil
}

// GenerateAccessToken creates a signed token with default 15m expiry.
func (m *JWTManager) GenerateAccessToken(claims Claims, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = 15 * time.Minute
	}
	claims.TokenType = TokenTypeAccess
	return m.generateToken(claims, ttl)
}

// GenerateRefreshToken creates a signed refresh token with default 14d expiry.
func (m *JWTManager) GenerateRefreshToken(claims Claims, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = 14 * 24 * time.Hour
	}
	claims.TokenType = TokenTypeRefresh
	return m.generateToken(claims, ttl)
}

// ValidateToken validates token signature and expiry.
func (m *JWTManager) ValidateToken(token string) (*Claims, error) {
	if m == nil || len(m.secret) == 0 {
		return nil, errors.New("jwt manager not initialized")
	}
	header, rest, ok := strings.Cut(token, ".")
	if !ok {
		return nil, errInvalidTokenFormat
	}
	payloadPart, signaturePart, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(signaturePart, ".") {
		return nil, errInvalidTokenFormat
	}

	unsigned := header + "." + payloadPart
	expected := m.sign(unsigned)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signaturePart)) != 1 {
		return nil, errInvalidSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	nowUnix := time.Now().UTC().Unix()
	leeway := int64(ClockSkewLeeway.Seconds())
	if claims.ExpiresAt != 0 && nowUnix > claims.ExpiresAt+leeway {
		return nil, errTokenExpired
	}
	// The other half of the same skew problem. `iat` was minted by GenerateToken
	// and never checked, so a token stamped arbitrarily far in the future was
	// accepted for its whole lifetime — from a badly skewed issuer, or from an
	// attacker who controls the claim, as a way to extend validity past what the
	// issuer intended. Rejecting a future `iat` beyond the same leeway closes
	// that without punishing ordinary drift.
	if claims.IssuedAt != 0 && claims.IssuedAt-leeway > nowUnix {
		return nil, errTokenIssuedInFuture
	}
	return &claims, nil
}

// ValidateRefreshToken validates a refresh token signature, expiry, and type.
func (m *JWTManager) ValidateRefreshToken(token string) (*Claims, error) {
	claims, err := m.ValidateToken(token)
	if err != nil {
		return nil, err
	}
	if !claims.IsRefreshToken() {
		return nil, errInvalidTokenType
	}
	return claims, nil
}

// ParseBearerToken extracts bearer token from Authorization header.
func ParseBearerToken(header string) (string, error) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "bearer") {
		return "", errors.New("invalid authorization header")
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.Contains(token, " ") {
		return "", errors.New("missing bearer token")
	}
	return token, nil
}

func (m *JWTManager) generateToken(claims Claims, ttl time.Duration) (string, error) {
	if m == nil || len(m.secret) == 0 {
		return "", errors.New("jwt manager not initialized")
	}
	if claims.UserID == "" {
		return "", errors.New("user_id is required")
	}
	now := time.Now().UTC()
	claims.IssuedAt = now.Unix()
	claims.ExpiresAt = now.Add(ttl).Unix()

	headerBytes, err := json.Marshal(map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	})
	if err != nil {
		return "", err
	}
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	header := base64.RawURLEncoding.EncodeToString(headerBytes)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	unsigned := header + "." + payload
	signature := m.sign(unsigned)
	return unsigned + "." + signature, nil
}

func (m *JWTManager) sign(unsigned string) string {
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(unsigned))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (c *Claims) IsRefreshToken() bool {
	if c == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(c.TokenType), TokenTypeRefresh) {
		return true
	}
	return strings.TrimSpace(c.TokenType) == "" &&
		strings.TrimSpace(c.Email) == "" &&
		strings.TrimSpace(c.Role) == "" &&
		len(c.Capabilities) == 0
}

// signClaimsForTest signs claims verbatim, without the timestamp stamping that
// generateToken applies. It exists so validation tests can construct tokens with
// deliberately skewed or forged `iat`/`exp` values that the generator would
// otherwise overwrite.
func (m *JWTManager) signClaimsForTest(claims Claims) (string, error) {
	headerBytes, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString(headerBytes)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	unsigned := header + "." + payload
	return unsigned + "." + m.sign(unsigned), nil
}
