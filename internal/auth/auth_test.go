package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-key"

func TestGenerateToken(t *testing.T) {
	a := New(testSecret, time.Hour)
	token, err := a.GenerateToken("abc123", "user@example.com")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
}

func TestValidateToken(t *testing.T) {
	a := New(testSecret, time.Hour)
	token, _ := a.GenerateToken("acc123", "user@example.com")

	claims, err := a.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if claims.AccountID != "acc123" {
		t.Errorf("AccountID = %q, want %q", claims.AccountID, "acc123")
	}
	if claims.Address != "user@example.com" {
		t.Errorf("Address = %q, want %q", claims.Address, "user@example.com")
	}
}

func TestValidateToken_Expired(t *testing.T) {
	a := New(testSecret, -time.Hour) // already expired
	token, _ := a.GenerateToken("acc123", "user@example.com")

	_, err := a.ValidateToken(token)
	if err != ErrExpiredToken {
		t.Errorf("err = %v, want ErrExpiredToken", err)
	}
}

func TestValidateToken_InvalidSignature(t *testing.T) {
	a := New(testSecret, time.Hour)
	token, _ := a.GenerateToken("acc123", "user@example.com")

	other := New("wrong-secret", time.Hour)
	_, err := other.ValidateToken(token)
	if err != ErrInvalidToken {
		t.Errorf("err = %v, want ErrInvalidToken", err)
	}
}

func TestValidateToken_MalformedToken(t *testing.T) {
	a := New(testSecret, time.Hour)
	_, err := a.ValidateToken("not-a-jwt")
	if err != ErrInvalidToken {
		t.Errorf("err = %v, want ErrInvalidToken", err)
	}
}

func TestValidateToken_EmptyToken(t *testing.T) {
	a := New(testSecret, time.Hour)
	_, err := a.ValidateToken("")
	if err != ErrInvalidToken {
		t.Errorf("err = %v, want ErrInvalidToken", err)
	}
}

func TestValidateToken_WrongSigningMethod(t *testing.T) {
	// Create a token with a non-HMAC method (none)
	claims := &Claims{
		AccountID: "acc123",
		Address:   "user@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	signed, _ := token.SignedString(jwt.UnsafeAllowNoneSignatureType)

	a := New(testSecret, time.Hour)
	_, err := a.ValidateToken(signed)
	if err != ErrInvalidToken {
		t.Errorf("err = %v, want ErrInvalidToken", err)
	}
}

func TestGenerateToken_ClaimsTimestamps(t *testing.T) {
	expiry := 2 * time.Hour
	a := New(testSecret, expiry)
	// JWT timestamps are truncated to seconds
	before := time.Now().Add(-time.Second)
	token, _ := a.GenerateToken("id1", "a@b.com")
	after := time.Now().Add(time.Second)

	claims, _ := a.ValidateToken(token)

	iat := claims.IssuedAt.Time
	exp := claims.ExpiresAt.Time
	nbf := claims.NotBefore.Time

	if iat.Before(before) || iat.After(after) {
		t.Errorf("IssuedAt %v not between %v and %v", iat, before, after)
	}
	if nbf.Before(before) || nbf.After(after) {
		t.Errorf("NotBefore %v not between %v and %v", nbf, before, after)
	}
	expectedExp := iat.Add(expiry)
	if exp.Before(expectedExp.Add(-2*time.Second)) || exp.After(expectedExp.Add(2*time.Second)) {
		t.Errorf("ExpiresAt %v, expected ~%v", exp, expectedExp)
	}
}
