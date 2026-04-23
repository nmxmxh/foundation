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
