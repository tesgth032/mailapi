package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("expired token")
)

type Claims struct {
	AccountID string `json:"accountId"`
	Address   string `json:"address"`
	jwt.RegisteredClaims
}

type Auth struct {
	secret []byte
	expiry time.Duration
	cache  *tokenCache
}

func New(secret string, expiry time.Duration) *Auth {
	return &Auth{
		secret: []byte(secret),
		expiry: expiry,
		cache:  newTokenCache(50000, 30*time.Second),
	}
}

func (a *Auth) GenerateToken(accountID, address string) (string, error) {
	return a.GenerateTokenWithExpiry(accountID, address, a.expiry)
}

func (a *Auth) GenerateTokenWithExpiry(accountID, address string, expiry time.Duration) (string, error) {
	if expiry <= 0 {
		expiry = a.expiry
	}

	now := time.Now()
	claims := &Claims{
		AccountID: accountID,
		Address:   address,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(expiry)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.secret)
}

func (a *Auth) ValidateToken(tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, ErrInvalidToken
	}

	if a.cache != nil {
		if c, ok := a.cache.Get(tokenString, time.Now()); ok {
			return c, nil
		}
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return a.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrExpiredToken
		}
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	if a.cache != nil {
		a.cache.Put(tokenString, claims)
	}

	return claims, nil
}
