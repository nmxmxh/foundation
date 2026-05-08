package auth

import (
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

	token, err := manager.GenerateAccessToken(Claims{UserID: "usr_1"}, -time.Minute)
	if err != nil {
		t.Fatalf("generate token failed: %v", err)
	}
	if _, err := manager.ValidateToken(token); err == nil {
		t.Fatalf("expected expired token error")
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
