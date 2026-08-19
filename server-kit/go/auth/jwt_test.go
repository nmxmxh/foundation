package auth

import (
	"errors"
	"testing"
	"time"
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
