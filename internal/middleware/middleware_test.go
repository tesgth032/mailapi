package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mailapi/internal/auth"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- BearerAuth tests ---

func TestBearerAuth_NoHeader(t *testing.T) {
	a := auth.New("secret", time.Hour)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	BearerAuth(nil, a)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (pass through)", w.Code, http.StatusOK)
	}
}

func TestBearerAuth_ValidAPIKey(t *testing.T) {
	apiKeys := map[string]*APIKeyInfo{
		"sk_test123": {Name: "TestSK", Domains: []string{"example.com"}, RPMLimit: 100},
		"dk_test123": {Name: "TestDK", Domains: []string{"example.com"}, RPMLimit: 100},
	}
	a := auth.New("secret", time.Hour)

	for _, tc := range []struct {
		token    string
		wantName string
	}{
		{token: "sk_test123", wantName: "TestSK"},
		{token: "dk_test123", wantName: "TestDK"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer "+tc.token)

		BearerAuth(apiKeys, a)(c)

		if w.Code != http.StatusOK {
			t.Errorf("token=%q status=%d, want %d", tc.token, w.Code, http.StatusOK)
			continue
		}

		domains := GetAllowedDomains(c)
		if len(domains) != 1 || domains[0] != "example.com" {
			t.Errorf("token=%q domains=%v, want [example.com]", tc.token, domains)
		}

		name, _ := c.Get(CtxAPIKeyName)
		if name != tc.wantName {
			t.Errorf("token=%q name=%v, want %v", tc.token, name, tc.wantName)
		}
	}
}

func TestBearerAuth_BearerScheme_CaseAndWhitespace(t *testing.T) {
	apiKeys := map[string]*APIKeyInfo{
		"sk_test123": {Name: "TestSK", Domains: []string{"example.com"}, RPMLimit: 100},
	}
	a := auth.New("secret", time.Hour)

	for _, header := range []string{
		"bearer sk_test123",
		"Bearer    sk_test123",
		"BEARER\tsk_test123",
		"  Bearer\t  sk_test123  ",
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", header)

		BearerAuth(apiKeys, a)(c)

		if w.Code != http.StatusOK {
			t.Fatalf("header=%q status=%d, want %d", header, w.Code, http.StatusOK)
		}
		name, _ := c.Get(CtxAPIKeyName)
		if name != "TestSK" {
			t.Fatalf("header=%q name=%v, want %v", header, name, "TestSK")
		}
	}
}

func TestBearerAuth_InvalidAPIKey(t *testing.T) {
	apiKeys := map[string]*APIKeyInfo{
		"sk_valid": {Name: "ValidSK", Domains: []string{"*"}},
		"dk_valid": {Name: "ValidDK", Domains: []string{"*"}},
	}
	a := auth.New("secret", time.Hour)

	for _, token := range []string{"sk_invalid", "dk_invalid"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Request.Header.Set("Authorization", "Bearer "+token)

		BearerAuth(apiKeys, a)(c)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("token=%q status=%d, want %d", token, w.Code, http.StatusUnauthorized)
		}
	}
}

func TestBearerAuth_ValidJWT(t *testing.T) {
	a := auth.New("secret", time.Hour)
	token, _ := a.GenerateToken("acc123", "user@example.com")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)

	BearerAuth(nil, a)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	accountID, exists := c.Get("accountId")
	if !exists || accountID != "acc123" {
		t.Errorf("accountId = %v (exists=%v), want acc123", accountID, exists)
	}

	address, exists := c.Get("address")
	if !exists || address != "user@example.com" {
		t.Errorf("address = %v (exists=%v), want user@example.com", address, exists)
	}

	// JWT users get scoped to their own domain
	domains := GetAllowedDomains(c)
	if len(domains) != 1 || domains[0] != "example.com" {
		t.Errorf("domains = %v, want [example.com]", domains)
	}
}

func TestBearerAuth_InvalidJWT(t *testing.T) {
	a := auth.New("secret", time.Hour)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Authorization", "Bearer invalid-jwt-token")

	BearerAuth(nil, a)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestBearerAuth_ExpiredJWT(t *testing.T) {
	expiredAuth := auth.New("secret", -time.Hour)
	token, _ := expiredAuth.GenerateToken("id1", "user@example.com")

	validAuth := auth.New("secret", time.Hour)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Authorization", "Bearer "+token)

	BearerAuth(nil, validAuth)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestBearerAuth_NonBearerFormat(t *testing.T) {
	a := auth.New("secret", time.Hour)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Header.Set("Authorization", "Basic abc123")

	BearerAuth(nil, a)(c)

	// Non-Bearer auth is passed through, not rejected
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (pass through)", w.Code, http.StatusOK)
	}
}

func TestBearerAuth_NoKeysWildcard(t *testing.T) {
	a := auth.New("secret", time.Hour)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	// nil apiKeys → wildcard access
	BearerAuth(nil, a)(c)

	domains := GetAllowedDomains(c)
	found := false
	for _, d := range domains {
		if d == "*" {
			found = true
		}
	}
	if !found {
		t.Errorf("domains = %v, want [*]", domains)
	}
}

// --- AuthRequired tests ---

func TestAuthRequired_WithJWT(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("accountId", "acc123")

	AuthRequired()(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAuthRequired_NoJWT(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	AuthRequired()(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// --- RequireAPIKey tests ---

func TestRequireAPIKey_NoKeysConfigured(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	RequireAPIKey(false)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRequireAPIKey_KeyPresent(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set(CtxAPIKey, "sk_test")

	RequireAPIKey(true)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRequireAPIKey_JWTPresent(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("accountId", "acc123")

	RequireAPIKey(true)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRequireAPIKey_NeitherPresent(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	RequireAPIKey(true)(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// --- RateLimit tests ---

type mockRateLimiter struct {
	allowed bool
	err     error
}

func (m *mockRateLimiter) CheckRateLimit(_ context.Context, _ string, _ int64, _ time.Duration) (bool, error) {
	return m.allowed, m.err
}

func TestRateLimit_Allowed(t *testing.T) {
	rl := &mockRateLimiter{allowed: true}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	RateLimit(rl, 100, time.Minute)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRateLimit_Denied(t *testing.T) {
	rl := &mockRateLimiter{allowed: false}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	RateLimit(rl, 100, time.Minute)(c)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
}

func TestRateLimit_ErrorFailsOpen(t *testing.T) {
	rl := &mockRateLimiter{allowed: false, err: context.DeadlineExceeded}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	RateLimit(rl, 100, time.Minute)(c)

	// Should fail open on Redis error
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (fail open)", w.Code, http.StatusOK)
	}
}

// --- APIKeyRateLimit tests ---

type mockKeyRateLimiter struct {
	keyAllowed    bool
	keyErr        error
	domainAllowed bool
	domainErr     error
}

func (m *mockKeyRateLimiter) CheckKeyRateLimit(_ context.Context, _ string, _ int64, _ time.Duration) (bool, error) {
	return m.keyAllowed, m.keyErr
}

func (m *mockKeyRateLimiter) CheckKeyDomainRateLimit(_ context.Context, _, _ string, _ int64, _ time.Duration) (bool, error) {
	return m.domainAllowed, m.domainErr
}

func TestAPIKeyRateLimit_NoKey(t *testing.T) {
	krl := &mockKeyRateLimiter{keyAllowed: false}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)

	APIKeyRateLimit(krl)(c)

	// No API key in context → pass through
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAPIKeyRateLimit_Allowed(t *testing.T) {
	krl := &mockKeyRateLimiter{keyAllowed: true}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set(CtxAPIKeyInfo, &APIKeyInfo{RPMLimit: 100})
	c.Set(CtxAPIKey, "sk_test")

	APIKeyRateLimit(krl)(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAPIKeyRateLimit_Denied(t *testing.T) {
	krl := &mockKeyRateLimiter{keyAllowed: false}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set(CtxAPIKeyInfo, &APIKeyInfo{RPMLimit: 100})
	c.Set(CtxAPIKey, "sk_test")

	APIKeyRateLimit(krl)(c)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", w.Code, http.StatusTooManyRequests)
	}
}
