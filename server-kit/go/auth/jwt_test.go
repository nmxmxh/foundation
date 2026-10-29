package auth

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/scratch"
)

func TestJWTManagerGenerateAndValidate(t *testing.T) {
	manager, err := NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager failed: %v", err)
	}

	token, err := manager.GenerateAccessToken(Claims{
		UserID:         "usr_1",
		Role:           "dispatcher",
		OrganizationID: "org_1",
		Capabilities:   []string{"operations.dispatch"},
	}, time.Minute)
	if err != nil {
		t.Fatalf("generate token failed: %v", err)
	}

	claims, err := manager.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate token failed: %v", err)
	}
	if claims.UserID != "usr_1" {
		t.Fatalf("unexpected user_id")
	}
}

func TestJWTManagerRejectsExpired(t *testing.T) {
	manager, err := NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager failed: %v", err)
	}

	// Expire well beyond the clock-skew leeway so it is unambiguously rejected.
	token, err := manager.GenerateAccessToken(Claims{UserID: "usr_1"}, -(ClockSkewLeeway + time.Minute))
	if err != nil {
		t.Fatalf("generate token failed: %v", err)
	}
	if _, err := manager.ValidateToken(token); err == nil {
		t.Fatalf("expected expired token error")
	}
}

func TestJWTManagerToleratesClockSkewWithinLeeway(t *testing.T) {
	manager, err := NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager failed: %v", err)
	}

	// A token that expired a few seconds ago (less than the leeway) simulates a
	// validator whose clock runs slightly ahead of the issuer. It must still be
	// accepted to avoid spurious unauthorized/logout right after token rotation.
	token, err := manager.GenerateAccessToken(Claims{UserID: "usr_1"}, -(ClockSkewLeeway / 2))
	if err != nil {
		t.Fatalf("generate token failed: %v", err)
	}
	if _, err := manager.ValidateToken(token); err != nil {
		t.Fatalf("token within leeway should validate, got: %v", err)
	}
}

func TestParseBearerToken(t *testing.T) {
	token, err := ParseBearerToken("Bearer abc.def.ghi")
	if err != nil {
		t.Fatalf("parse bearer failed: %v", err)
	}
	if token != "abc.def.ghi" {
		t.Fatalf("unexpected token parse result")
	}
}

func TestValidateRefreshTokenAcceptsLegacyRefreshClaims(t *testing.T) {
	manager, err := NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager failed: %v", err)
	}

	token, err := manager.generateToken(Claims{UserID: "usr_1"}, time.Minute)
	if err != nil {
		t.Fatalf("generate token failed: %v", err)
	}

	claims, err := manager.ValidateRefreshToken(token)
	if err != nil {
		t.Fatalf("validate refresh token failed: %v", err)
	}
	if claims.UserID != "usr_1" {
		t.Fatalf("unexpected user_id")
	}
}

func TestValidateRefreshTokenRejectsAccessTokens(t *testing.T) {
	manager, err := NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager failed: %v", err)
	}

	token, err := manager.GenerateAccessToken(Claims{UserID: "usr_1", Role: "admin"}, time.Minute)
	if err != nil {
		t.Fatalf("generate token failed: %v", err)
	}

	if _, err := manager.ValidateRefreshToken(token); err == nil {
		t.Fatalf("expected refresh validation to reject access token")
	}
}

func TestJWTManagerRefreshAndValidationEdges(t *testing.T) {
	if _, err := NewJWTManager("short"); err == nil {
		t.Fatalf("expected short secret rejection")
	}
	manager, err := NewJWTManager("this-is-a-very-secure-secret")
	if err != nil {
		t.Fatalf("new jwt manager failed: %v", err)
	}
	if _, err := (*JWTManager)(nil).GenerateAccessToken(Claims{UserID: "usr_1"}, time.Minute); err == nil {
		t.Fatalf("expected nil manager generate error")
	}
	if _, err := manager.GenerateAccessToken(Claims{}, time.Minute); err == nil {
		t.Fatalf("expected missing user id error")
	}

	token, err := manager.GenerateRefreshToken(Claims{UserID: "usr_1"}, 0)
	if err != nil {
		t.Fatalf("generate refresh token failed: %v", err)
	}
	claims, err := manager.ValidateRefreshToken(token)
	if err != nil {
		t.Fatalf("validate refresh token failed: %v", err)
	}
	if !claims.IsRefreshToken() || claims.TokenType != TokenTypeRefresh {
		t.Fatalf("expected refresh claims: %+v", claims)
	}
	if (*Claims)(nil).IsRefreshToken() {
		t.Fatalf("nil claims should not be refresh")
	}
	accessClaims := Claims{TokenType: " ACCESS ", Email: "a@example.com"}
	if accessClaims.IsRefreshToken() {
		t.Fatalf("access claims should not be refresh")
	}

	for _, invalid := range []string{
		"",
		"Bearer",
		"Basic abc.def",
		"Bearer ",
		"Bearer abc def",
	} {
		if _, err := ParseBearerToken(invalid); err == nil {
			t.Fatalf("expected invalid bearer header %q", invalid)
		}
	}
	if _, err := (*JWTManager)(nil).ValidateToken(token); err == nil {
		t.Fatalf("expected nil manager validate error")
	}
	for _, invalid := range []string{"one-part", "a.b.c.d", "a.b.c"} {
		if _, err := manager.ValidateToken(invalid); err == nil {
			t.Fatalf("expected invalid token error for %q", invalid)
		}
	}
}

// TestJWTManagerRejectsFutureIssuedTokens closes the other half of the
// clock-skew problem. `iat` is minted by generateToken and was never validated,
// so a token stamped far in the future was accepted for its whole lifetime.
// That is not only a skew bug: an issuer under an attacker's influence could
// post-date `iat` to extend a token's useful life past what was intended.
func TestJWTManagerRejectsFutureIssuedTokens(t *testing.T) {
	manager, err := NewJWTManager("a-sufficiently-long-secret-value")
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	// Forge a token whose iat is well beyond the leeway, signed correctly so the
	// only thing that can reject it is the iat check itself.
	future := time.Now().UTC().Add(ClockSkewLeeway + 10*time.Minute)
	forged := Claims{
		UserID:    "usr_1",
		TokenType: TokenTypeAccess,
		IssuedAt:  future.Unix(),
		ExpiresAt: future.Add(time.Hour).Unix(),
	}
	token, err := manager.signClaimsForTest(forged)
	if err != nil {
		t.Fatalf("sign forged claims: %v", err)
	}

	if _, err := manager.ValidateToken(token); !errors.Is(err, errTokenIssuedInFuture) {
		t.Fatalf("future-issued token should be rejected, got: %v", err)
	}
}

// TestJWTManagerToleratesFutureIssuedWithinLeeway is the counterpart: ordinary
// drift must not be punished, or the fix trades one spurious logout for another.
func TestJWTManagerToleratesFutureIssuedWithinLeeway(t *testing.T) {
	manager, err := NewJWTManager("a-sufficiently-long-secret-value")
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	skewed := time.Now().UTC().Add(ClockSkewLeeway / 2)
	claims := Claims{
		UserID:    "usr_1",
		TokenType: TokenTypeAccess,
		IssuedAt:  skewed.Unix(),
		ExpiresAt: skewed.Add(time.Hour).Unix(),
	}
	token, err := manager.signClaimsForTest(claims)
	if err != nil {
		t.Fatalf("sign claims: %v", err)
	}

	if _, err := manager.ValidateToken(token); err != nil {
		t.Fatalf("iat within leeway should validate, got: %v", err)
	}
}

// TestJWTManagerAcceptsTokensWithoutIssuedAt keeps the check optional: a token
// carrying no iat at all is unchanged by this validation, matching how the
// expiry check treats a zero exp.
func TestJWTManagerAcceptsTokensWithoutIssuedAt(t *testing.T) {
	manager, err := NewJWTManager("a-sufficiently-long-secret-value")
	if err != nil {
		t.Fatalf("NewJWTManager: %v", err)
	}

	token, err := manager.signClaimsForTest(Claims{
		UserID:    "usr_1",
		TokenType: TokenTypeAccess,
		ExpiresAt: time.Now().UTC().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("sign claims: %v", err)
	}

	if _, err := manager.ValidateToken(token); err != nil {
		t.Fatalf("token without iat should validate, got: %v", err)
	}
}

func TestJWTManagerHandlesLongSecretAndEscapedClaims(t *testing.T) {
	longSecret := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef-extra-bytes"
	manager, err := NewJWTManager(longSecret)
	if err != nil {
		t.Fatalf("NewJWTManager with long secret failed: %v", err)
	}

	want := Claims{
		UserID:         "usr_\"quoted\"_\\slash",
		Email:          "user+tag@example.com",
		Role:           "admin",
		OrganizationID: "org_1",
		SessionID:      "sess_1",
		DeviceID:       "dev_1",
		Capabilities:   []string{"cap.read", "cap.\"write\""},
	}
	token, err := manager.GenerateAccessToken(want, time.Minute)
	if err != nil {
		t.Fatalf("GenerateAccessToken failed: %v", err)
	}

	got, err := manager.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if got.UserID != want.UserID || got.Email != want.Email || len(got.Capabilities) != 2 || got.Capabilities[1] != want.Capabilities[1] {
		t.Fatalf("unexpected round-trip claims: got %+v, want %+v", got, want)
	}

	var parsed Claims
	if !parseClaimsFast(`{"user_id":"u","email":"e","role":"r","organization_id":"o","device_id":"d","session_id":"s","token_type":"custom","capabilities":[],"iat":-5,"exp":10}`, &parsed) {
		t.Fatalf("expected parseClaimsFast to succeed on full flat object")
	}
	if !parseClaimsFast(`{}`, &parsed) {
		t.Fatalf("expected parseClaimsFast to succeed on empty object")
	}
	for _, bad := range []string{
		``, `[]`, `{1:2}`, `{"user_id"}`, `{"user_id":}`, `{"user_id":"a" "b":1}`, `{"user_id":"a",}`,
		`{"unknown":"x"}`, `{"user_id":123}`, `{"iat":"x"}`, `{"iat":-}`, `{"capabilities":[1]}`,
		`{"capabilities":["a" "b"]}`, `{"capabilities":["a",]}`, `{"capabilities":["a"`,
	} {
		if parseClaimsFast(bad, &parsed) {
			t.Fatalf("expected parseClaimsFast(%q) to return false", bad)
		}
	}
}

func TestValidateTokenAllocationBudget(t *testing.T) {
	manager, err := NewJWTManager("this-is-a-very-secure-secret-key-for-jwt")
	if err != nil {
		t.Fatalf("NewJWTManager failed: %v", err)
	}
	token, err := manager.GenerateAccessToken(Claims{
		UserID:         "usr_01J8X6Z2Y9W4Q7R5T3V1N0M8K2",
		Role:           "operator",
		OrganizationID: "org_01J8X6Z2Y9W4Q7R5T3V1N0M8K2",
		SessionID:      "sess_01J8X6Z2Y9W4Q7R5T3V1N0M8K2",
		Capabilities:   []string{"orders.read", "orders.write"},
	}, 15*time.Minute)
	if err != nil {
		t.Fatalf("GenerateAccessToken failed: %v", err)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := manager.ValidateToken(token); err != nil {
			t.Fatalf("ValidateToken failed: %v", err)
		}
	})
	if allocs > 8 {
		t.Fatalf("ValidateToken exceeded allocation budget: got %.1f allocs/run, want <= 8", allocs)
	}
}

func TestValidateTokenWithArena_ZeroAllocations(t *testing.T) {
	manager, err := NewJWTManager("this-is-a-very-secure-secret-key-for-jwt")
	if err != nil {
		t.Fatalf("NewJWTManager failed: %v", err)
	}
	expectedClaims := Claims{
		UserID:         "usr_01J8X6Z2Y9W4Q7R5T3V1N0M8K2",
		Role:           "operator",
		OrganizationID: "org_01J8X6Z2Y9W4Q7R5T3V1N0M8K2",
		SessionID:      "sess_01J8X6Z2Y9W4Q7R5T3V1N0M8K2",
		Capabilities:   []string{"orders.read", "orders.write"},
	}
	token, err := manager.GenerateAccessToken(expectedClaims, 15*time.Minute)
	if err != nil {
		t.Fatalf("GenerateAccessToken failed: %v", err)
	}

	arena := scratch.New(4096)

	// Parity check
	claims, err := manager.ValidateTokenWithArena(arena, token)
	if err != nil {
		t.Fatalf("ValidateTokenWithArena failed: %v", err)
	}
	if claims.UserID != expectedClaims.UserID || claims.Role != expectedClaims.Role {
		t.Fatalf("claims mismatch: got %+v, want %+v", claims, expectedClaims)
	}
	if len(claims.Capabilities) != 2 || claims.Capabilities[0] != "orders.read" {
		t.Fatalf("capabilities mismatch: %+v", claims.Capabilities)
	}

	// ValidateTokenInto check
	var target Claims
	if err := manager.ValidateTokenInto(&target, token); err != nil {
		t.Fatalf("ValidateTokenInto failed: %v", err)
	}
	if target.UserID != expectedClaims.UserID {
		t.Fatalf("target mismatch: %+v", target)
	}

	// Nil arena fallback
	nilClaims, err := manager.ValidateTokenWithArena(nil, token)
	if err != nil || nilClaims.UserID != expectedClaims.UserID {
		t.Fatalf("nil arena fallback failed")
	}

	// Tiny arena fallback
	tinyArena := scratch.New(8)
	tinyClaims, err := manager.ValidateTokenWithArena(tinyArena, token)
	if err != nil || tinyClaims.UserID != expectedClaims.UserID {
		t.Fatalf("tiny arena fallback failed")
	}

	// Nil target check
	if err := manager.ValidateTokenInto(nil, token); err == nil {
		t.Fatalf("expected error for nil target")
	}

	// Zero allocations check!
	allocs := testing.AllocsPerRun(200, func() {
		arena.Reset()
		if _, err := manager.ValidateTokenWithArena(arena, token); err != nil {
			t.Fatalf("ValidateTokenWithArena failed: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("expected 0 allocs for ValidateTokenWithArena, got %f", allocs)
	}
}

func TestValidateTokenRejectsRefreshTokens(t *testing.T) {
	manager, err := NewJWTManager("super-secret-key-at-least-16-bytes")
	if err != nil {
		t.Fatalf("new jwt manager failed: %v", err)
	}

	refreshToken, err := manager.GenerateRefreshToken(Claims{UserID: "usr_refresh_1"}, time.Hour)
	if err != nil {
		t.Fatalf("generate refresh token failed: %v", err)
	}

	if _, err := manager.ValidateToken(refreshToken); !errors.Is(err, errInvalidTokenType) {
		t.Fatalf("expected ValidateToken to reject refresh token with errInvalidTokenType, got %v", err)
	}

	var target Claims
	if err := manager.ValidateTokenInto(&target, refreshToken); !errors.Is(err, errInvalidTokenType) {
		t.Fatalf("expected ValidateTokenInto to reject refresh token, got %v", err)
	}

	arena := scratch.New(4096)
	if _, err := manager.ValidateTokenWithArena(arena, refreshToken); !errors.Is(err, errInvalidTokenType) {
		t.Fatalf("expected ValidateTokenWithArena to reject refresh token, got %v", err)
	}
}

func TestValidateTokenEmptyPayloadNoPanic(t *testing.T) {
	manager, err := NewJWTManager("super-secret-key-at-least-16-bytes")
	if err != nil {
		t.Fatalf("new jwt manager failed: %v", err)
	}

	header := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"
	unsigned := header + "."
	sig := manager.sign(unsigned)
	emptyPayloadToken := unsigned + "." + sig

	arena := scratch.New(4096)
	if _, err := manager.ValidateTokenWithArena(arena, emptyPayloadToken); err == nil {
		t.Fatalf("expected error for empty payload token")
	}

	if _, err := manager.ValidateToken(emptyPayloadToken); err == nil {
		t.Fatalf("expected error for empty payload token")
	}
}

func TestValidateTokenWithArena_DirtyMemoryWithLargePayload(t *testing.T) {
	manager, err := NewJWTManager("super-secret-key-at-least-16-bytes")
	if err != nil {
		t.Fatalf("new jwt manager failed: %v", err)
	}

	caps := make([]string, 50)
	for i := range caps {
		caps[i] = fmt.Sprintf("domain.action_capability_item_%d", i)
	}
	largeToken, err := manager.GenerateAccessToken(Claims{
		UserID:       "usr_large_1",
		Role:         "customer",
		Capabilities: caps,
	}, time.Hour)
	if err != nil {
		t.Fatalf("generate token failed: %v", err)
	}

	// Create an arena and deliberately poison it with 0x80 bytes to simulate
	// previous columnar/SWAR scanner reuse from sync.Pool.
	buf := make([]byte, 64*1024)
	for i := range buf {
		buf[i] = 0x80
	}
	arena := scratch.NewWithBuffer(buf)

	claims, err := manager.ValidateTokenWithArena(arena, largeToken)
	if err != nil {
		t.Fatalf("ValidateTokenWithArena failed: %v", err)
	}
	if len(claims.Capabilities) != 50 {
		t.Fatalf("expected 50 capabilities, got %d", len(claims.Capabilities))
	}
	for i, c := range claims.Capabilities {
		expected := fmt.Sprintf("domain.action_capability_item_%d", i)
		if c != expected {
			t.Fatalf("capability %d mismatch: got %q, want %q", i, c, expected)
		}
	}
}
