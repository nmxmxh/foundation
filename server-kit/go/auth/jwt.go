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
	"unsafe"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
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
	secret    []byte
	ipad      [64]byte
	opad      [64]byte
	padsReady bool
}

// NewJWTManager creates a token manager with HMAC signing.
func NewJWTManager(secret string) (*JWTManager, error) {
	if len(strings.TrimSpace(secret)) < 16 {
		return nil, errors.New("jwt secret must be at least 16 characters")
	}
	manager := &JWTManager{secret: []byte(secret)}
	manager.initPads()
	return manager, nil
}

func (m *JWTManager) initPads() {
	var keyBlock [64]byte
	if len(m.secret) > 64 {
		sum := sha256.Sum256(m.secret)
		copy(keyBlock[:], sum[:])
	} else {
		copy(keyBlock[:], m.secret)
	}
	for i := range 64 {
		m.ipad[i] = keyBlock[i] ^ 0x36
		m.opad[i] = keyBlock[i] ^ 0x5c
	}
	m.padsReady = true
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
	var claims Claims
	if err := m.ValidateTokenInto(&claims, token); err != nil {
		return nil, err
	}
	return &claims, nil
}

// ValidateTokenWithArena validates token signature and expiry, allocating the Claims
// structure and any string fields from the provided scratch arena.
// When arena is nil or full, it falls back to standard ValidateToken.
func (m *JWTManager) ValidateTokenWithArena(arena *scratch.Arena, token string) (*Claims, error) {
	if arena == nil {
		return m.ValidateToken(token)
	}
	claims, err := scratch.Alloc[Claims](arena)
	if err != nil {
		return m.ValidateToken(token)
	}
	if err := m.validateTokenIntoArena(claims, token, arena); err != nil {
		return nil, err
	}
	if claims.IsRefreshToken() {
		return nil, errInvalidTokenType
	}
	return claims, nil
}

// ValidateTokenInto validates token signature and expiry, unmarshaling claims directly into target.
// When target is caller-allocated (on stack or arena), this eliminates the return pointer allocation.
func (m *JWTManager) ValidateTokenInto(target *Claims, token string) error {
	if err := m.validateTokenIntoArena(target, token, nil); err != nil {
		return err
	}
	if target.IsRefreshToken() {
		return errInvalidTokenType
	}
	return nil
}

func (m *JWTManager) validateTokenIntoArena(target *Claims, token string, arena *scratch.Arena) error {
	if m == nil || len(m.secret) == 0 {
		return errors.New("jwt manager not initialized")
	}
	if target == nil {
		return errors.New("target claims pointer is required")
	}
	header, rest, ok := strings.Cut(token, ".")
	if !ok {
		return errInvalidTokenFormat
	}
	payloadPart, signaturePart, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(signaturePart, ".") {
		return errInvalidTokenFormat
	}

	unsigned := token[:len(header)+1+len(payloadPart)]
	if !m.verifySignature(unsigned, signaturePart) {
		return errInvalidSignature
	}

	if err := decodeAndUnmarshalClaimsWithArena(payloadPart, target, arena); err != nil {
		return err
	}

	nowUnix := time.Now().UTC().Unix()
	leeway := int64(ClockSkewLeeway.Seconds())
	if target.ExpiresAt != 0 && nowUnix > target.ExpiresAt+leeway {
		return errTokenExpired
	}
	if target.IssuedAt != 0 && target.IssuedAt-leeway > nowUnix {
		return errTokenIssuedInFuture
	}
	return nil
}

// ValidateRefreshToken validates a refresh token signature, expiry, and type.
func (m *JWTManager) ValidateRefreshToken(token string) (*Claims, error) {
	var claims Claims
	if err := m.validateTokenIntoArena(&claims, token, nil); err != nil {
		return nil, err
	}
	if !claims.IsRefreshToken() {
		return nil, errInvalidTokenType
	}
	return &claims, nil
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
	sum := m.hmacSum256(unsigned)
	var encoded [43]byte
	base64.RawURLEncoding.Encode(encoded[:], sum[:])
	return string(encoded[:])
}

func (m *JWTManager) verifySignature(unsigned, signaturePart string) bool {
	sum := m.hmacSum256(unsigned)
	var expected [43]byte
	base64.RawURLEncoding.Encode(expected[:], sum[:])
	if len(signaturePart) != len(expected) {
		return subtle.ConstantTimeCompare(expected[:], []byte(signaturePart)) == 1
	}
	var actual [43]byte
	copy(actual[:], signaturePart)
	return subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
}

func (m *JWTManager) hmacSum256(unsigned string) [32]byte {
	if m.padsReady && len(unsigned) <= 768 {
		var innerBuf [64 + 768]byte
		copy(innerBuf[:64], m.ipad[:])
		copy(innerBuf[64:], unsigned)
		innerSum := sha256.Sum256(innerBuf[:64+len(unsigned)])
		var outerBuf [64 + 32]byte
		copy(outerBuf[:64], m.opad[:])
		copy(outerBuf[64:], innerSum[:])
		return sha256.Sum256(outerBuf[:])
	}
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(unsigned))
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

func decodeAndUnmarshalClaimsWithArena(payloadPart string, claims *Claims, arena *scratch.Arena) error {
	if len(payloadPart) <= 768 {
		var srcBuf [768]byte
		var dstBuf [576]byte
		copy(srcBuf[:], payloadPart)
		n, err := base64.RawURLEncoding.Decode(dstBuf[:], srcBuf[:len(payloadPart)])
		if err != nil {
			return fmt.Errorf("decode payload: %w", err)
		}
		if n == 0 {
			return errInvalidTokenFormat
		}
		var data string
		if arena != nil {
			raw, err := arena.Alloc(n)
			if err == nil {
				copy(raw, dstBuf[:n])
				data = unsafe.String(&raw[0], n) // #nosec G103 -- zero-allocation string view into arena buffer.
			} else {
				data = string(dstBuf[:n])
			}
		} else {
			data = string(dstBuf[:n])
		}
		var parsed Claims
		if parseClaimsFastWithArena(data, &parsed, arena) {
			*claims = parsed
			return nil
		}
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	if len(payload) == 0 {
		return errInvalidTokenFormat
	}
	if err := json.Unmarshal(payload, claims); err != nil {
		return fmt.Errorf("unmarshal claims: %w", err)
	}
	return nil
}

// parseClaimsFast parses standard flat JWT claims without reflection allocations.
func parseClaimsFast(data string, claims *Claims) bool {
	return parseClaimsFastWithArena(data, claims, nil)
}

func parseClaimsFastWithArena(data string, claims *Claims, arena *scratch.Arena) bool {
	data = strings.TrimSpace(data)
	if len(data) < 2 || data[0] != '{' || data[len(data)-1] != '}' {
		return false
	}
	body := strings.TrimSpace(data[1 : len(data)-1])
	if len(body) == 0 {
		return true
	}
	for len(body) > 0 {
		key, rest, ok := cutJSONString(body)
		if !ok {
			return false
		}
		rest = strings.TrimSpace(rest)
		if len(rest) == 0 || rest[0] != ':' {
			return false
		}
		valRaw, next, ok := cutJSONFieldValue(strings.TrimSpace(rest[1:]))
		if !ok || !assignClaimFastWithArena(claims, key, valRaw, arena) {
			return false
		}
		body = next
	}
	return true
}

func assignClaimFastWithArena(claims *Claims, key, val string, arena *scratch.Arena) bool {
	if key == "capabilities" {
		return assignClaimCapabilitiesWithArena(&claims.Capabilities, val, arena)
	}
	return assignClaimFast(claims, key, val)
}

func cutJSONString(b string) (string, string, bool) {
	if len(b) < 2 || b[0] != '"' {
		return "", "", false
	}
	for i := 1; i < len(b); i++ {
		if b[i] == '\\' || b[i] < 0x20 {
			return "", "", false
		}
		if b[i] == '"' {
			return b[1:i], b[i+1:], true
		}
	}
	return "", "", false
}

func cutJSONFieldValue(b string) (string, string, bool) {
	if len(b) == 0 {
		return "", "", false
	}
	var end int
	switch b[0] {
	case '"':
		_, rest, ok := cutJSONString(b)
		if !ok {
			return "", "", false
		}
		end = len(b) - len(rest)
	case '[':
		idx := strings.IndexByte(b, ']')
		if idx < 0 {
			return "", "", false
		}
		end = idx + 1
	default:
		end = len(b)
		for i := 0; i < len(b); i++ {
			ch := b[i]
			if ch == ',' || ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
				end = i
				break
			}
		}
		if end == 0 {
			return "", "", false
		}
	}
	val := b[:end]
	rest := strings.TrimSpace(b[end:])
	if len(rest) == 0 {
		return val, "", true
	}
	if rest[0] != ',' {
		return "", "", false
	}
	next := strings.TrimSpace(rest[1:])
	if len(next) == 0 {
		return "", "", false
	}
	return val, next, true
}

func assignClaimFast(claims *Claims, key, val string) bool {
	switch key {
	case "user_id":
		return assignClaimString(&claims.UserID, val)
	case "email":
		return assignClaimString(&claims.Email, val)
	case "role":
		return assignClaimString(&claims.Role, val)
	case "organization_id":
		return assignClaimString(&claims.OrganizationID, val)
	case "device_id":
		return assignClaimString(&claims.DeviceID, val)
	case "session_id":
		return assignClaimString(&claims.SessionID, val)
	case "token_type":
		if val == `"access"` {
			claims.TokenType = TokenTypeAccess
			return true
		}
		if val == `"refresh"` {
			claims.TokenType = TokenTypeRefresh
			return true
		}
		return assignClaimString(&claims.TokenType, val)
	case "capabilities":
		return assignClaimCapabilities(&claims.Capabilities, val)
	case "iat":
		return assignClaimInt64(&claims.IssuedAt, val)
	case "exp":
		return assignClaimInt64(&claims.ExpiresAt, val)
	default:
		return false
	}
}

func assignClaimString(dst *string, val string) bool {
	str, rest, ok := cutJSONString(val)
	if !ok || len(rest) != 0 {
		return false
	}
	*dst = str
	return true
}

func assignClaimInt64(dst *int64, val string) bool {
	if len(val) == 0 || len(val) > 19 {
		return false
	}
	neg := false
	if val[0] == '-' {
		neg = true
		val = val[1:]
		if len(val) == 0 {
			return false
		}
	}
	var n int64
	for i := 0; i < len(val); i++ {
		ch := val[i]
		if ch < '0' || ch > '9' {
			return false
		}
		n = n*10 + int64(ch-'0')
	}
	if neg {
		n = -n
	}
	*dst = n
	return true
}

func assignClaimCapabilities(dst *[]string, val string) bool {
	return assignClaimCapabilitiesWithArena(dst, val, nil)
}

func assignClaimCapabilitiesWithArena(dst *[]string, val string, arena *scratch.Arena) bool {
	if len(val) < 2 || val[0] != '[' || val[len(val)-1] != ']' {
		return false
	}
	inner := strings.TrimSpace(val[1 : len(val)-1])
	if len(inner) == 0 {
		*dst = []string{}
		return true
	}
	count := 1 + strings.Count(inner, ",")
	var out []string
	if arena != nil {
		slice, err := scratch.AllocSlice[string](arena, count)
		if err == nil {
			out = slice[:0]
		} else {
			out = make([]string, 0, count)
		}
	} else {
		out = make([]string, 0, count)
	}
	for len(inner) > 0 {
		str, rest, ok := cutJSONString(inner)
		if !ok {
			return false
		}
		out = append(out, str)
		rest = strings.TrimSpace(rest)
		if len(rest) == 0 {
			break
		}
		if rest[0] != ',' {
			return false
		}
		inner = strings.TrimSpace(rest[1:])
		if len(inner) == 0 {
			return false
		}
	}
	*dst = out
	return true
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
