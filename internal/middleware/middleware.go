package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"mailapi/internal/auth"

	"github.com/gin-gonic/gin"
)

// Context key constants.
const (
	CtxAPIKeyDomains = "apiKeyDomains"
	CtxAPIKeyName    = "apiKeyName"
	CtxAPIKey        = "apiKey"
	CtxAPIKeyInfo    = "apiKeyInfo"
)

// APIKeyInfo holds metadata for a validated API key.
type APIKeyInfo struct {
	Name    string
	Domains []string
	// DomainSet 是 Domains 的加速结构（key=domain，小写）。当 Domains 包含 "*" 时为 nil。
	// 注意：该 map 只读（在启动时构建），可被并发安全读取。
	DomainSet map[string]struct{}
	// Wildcard 表示 Domains 包含 "*"，即允许所有域名。
	Wildcard     bool
	RPMLimit     int64
	DomainLimits map[string]int64
}

// RateLimitChecker is the interface required by the IP-based RateLimit middleware.
type RateLimitChecker interface {
	CheckRateLimit(ctx context.Context, key string, limit int64, window time.Duration) (bool, error)
}

// KeyRateLimitChecker is the interface for per-API-key rate limiting.
type KeyRateLimitChecker interface {
	CheckKeyRateLimit(ctx context.Context, apiKey string, limit int64, window time.Duration) (bool, error)
	CheckKeyDomainRateLimit(ctx context.Context, apiKey, domain string, limit int64, window time.Duration) (bool, error)
}

// BearerAuth parses the Authorization: Bearer header and handles both
// API keys and JWT tokens.
//
// API keys: default "sk_" prefix; legacy "dk_" prefix is also accepted.
// For API key tokens: validates against apiKeys map, sets API key context.
// For other tokens: validates as JWT, sets user context.
// If no header is present, passes through without rejection.
func BearerAuth(apiKeys map[string]*APIKeyInfo, a *auth.Auth) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Default wildcard access when no API keys configured
		if len(apiKeys) == 0 {
			c.Set(CtxAPIKeyDomains, []string{"*"})
		}

		header := c.GetHeader("Authorization")
		if header == "" {
			c.Next()
			return
		}

		token, ok := bearerTokenFromAuthorization(header)
		if !ok {
			// Not a Bearer token, pass through
			c.Next()
			return
		}

		if strings.HasPrefix(token, "sk_") || strings.HasPrefix(token, "dk_") {
			// API key authentication
			info, ok := apiKeys[token]
			if !ok {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"code": 401, "message": "invalid API key",
				})
				return
			}
			c.Set(CtxAPIKeyDomains, info.Domains)
			c.Set(CtxAPIKeyName, info.Name)
			c.Set(CtxAPIKey, token)
			c.Set(CtxAPIKeyInfo, info)
		} else {
			// JWT authentication
			claims, err := a.ValidateToken(token)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"code": 401, "message": "invalid or expired token",
				})
				return
			}
			c.Set("accountId", claims.AccountID)
			c.Set("address", claims.Address)
			// JWT users are scoped to their own domain
			domain := DomainFromAddress(claims.Address)
			if domain != "" {
				c.Set(CtxAPIKeyDomains, []string{domain})
			}
		}

		c.Next()
	}
}

// bearerTokenFromAuthorization 从 Authorization 头解析 Bearer token。
// 兼容：大小写不敏感、多空格、Tab。
// 返回 ok=false 表示该头不是 Bearer 方案。
func bearerTokenFromAuthorization(header string) (token string, ok bool) {
	header = strings.TrimSpace(header)
	if len(header) < 7 { // 至少 "Bearer " 7 个字符
		return "", false
	}

	// Scheme 大小写不敏感
	if !strings.EqualFold(header[:6], "Bearer") {
		return "", false
	}

	// "Bearer" 后必须跟空白（兼容多空格/Tab）
	if header[6] != ' ' && header[6] != '\t' {
		return "", false
	}

	return strings.TrimSpace(header[6:]), true
}

// RequireAPIKey ensures that an API key or JWT was provided when API keys
// are configured. If no API keys are configured, allows all requests with
// wildcard domain access (backward compatible).
func RequireAPIKey(hasAPIKeys bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hasAPIKeys {
			if _, exists := c.Get(CtxAPIKeyDomains); !exists {
				c.Set(CtxAPIKeyDomains, []string{"*"})
			}
			c.Next()
			return
		}

		// Accept either API key or JWT
		if _, exists := c.Get(CtxAPIKey); exists {
			c.Next()
			return
		}
		if c.GetString("accountId") != "" {
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"code": 401, "message": "API key required (Authorization: Bearer sk_... or dk_...)",
		})
	}
}

// AuthRequired ensures the request has valid JWT authentication
// (not just an API key). Must run after BearerAuth.
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("accountId") == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code": 401, "message": "authentication required",
			})
			return
		}
		c.Next()
	}
}

// RateLimit implements global rate limiting per client IP.
func RateLimit(ca RateLimitChecker, limit int64, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		allowed, err := ca.CheckRateLimit(c.Request.Context(), ip, limit, window)
		if err != nil {
			c.Next() // fail open
			return
		}
		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"code": 429, "message": "rate limit exceeded",
			})
			return
		}
		c.Next()
	}
}

// APIKeyRateLimit checks per-API-key RPM limits. Must run after BearerAuth.
func APIKeyRateLimit(ca KeyRateLimitChecker) gin.HandlerFunc {
	return func(c *gin.Context) {
		val, exists := c.Get(CtxAPIKeyInfo)
		if !exists {
			c.Next()
			return
		}
		info := val.(*APIKeyInfo)
		apiKey := c.GetString(CtxAPIKey)

		if info.RPMLimit > 0 {
			allowed, err := ca.CheckKeyRateLimit(c.Request.Context(), apiKey, info.RPMLimit, time.Minute)
			if err == nil && !allowed {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"code": 429, "message": "API key rate limit exceeded",
				})
				return
			}
		}

		c.Next()
	}
}

// CheckDomainRateLimit checks the per-API-key-per-domain rate limit.
// Returns true if the request is allowed. Should be called by handlers
// when the target domain is known.
func CheckDomainRateLimit(c *gin.Context, ca KeyRateLimitChecker, domain string) bool {
	val, exists := c.Get(CtxAPIKeyInfo)
	if !exists {
		return true
	}
	info := val.(*APIKeyInfo)
	apiKey := c.GetString(CtxAPIKey)

	limit, ok := info.DomainLimits[domain]
	if !ok || limit <= 0 {
		return true
	}

	allowed, err := ca.CheckKeyDomainRateLimit(c.Request.Context(), apiKey, domain, limit, time.Minute)
	if err != nil {
		return true // fail open
	}
	return allowed
}

// DomainScopeCheck verifies the JWT-authenticated user's domain is permitted
// by the current API key. Must run after BearerAuth.
func DomainScopeCheck() gin.HandlerFunc {
	return func(c *gin.Context) {
		address := c.GetString("address")
		if address != "" {
			domain := DomainFromAddress(address)
			if !IsDomainAllowed(c, domain) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"code": 403, "message": "domain not allowed for this API key",
				})
				return
			}
		}
		c.Next()
	}
}

// --- Helper functions ---

// DomainFromAddress extracts the domain part from an email address.
func DomainFromAddress(address string) string {
	if i := strings.LastIndexByte(address, '@'); i != -1 && i+1 < len(address) {
		return strings.ToLower(address[i+1:])
	}
	return ""
}

// IsDomainAllowed checks whether the given domain is permitted by the
// API key context on this request.
func IsDomainAllowed(c *gin.Context, domain string) bool {
	if info := GetAPIKeyInfo(c); info != nil {
		if info.Wildcard {
			return true
		}
		if info.DomainSet != nil {
			_, ok := info.DomainSet[domain]
			return ok
		}
		// 回退：无 DomainSet 时按 Domains 切片匹配（通常 domains 很少）。
		for _, d := range info.Domains {
			if d == "*" || d == domain {
				return true
			}
		}
		return false
	}

	for _, d := range GetAllowedDomains(c) {
		if d == "*" || d == domain {
			return true
		}
	}
	return false
}

// IsDomainExplicitlyAllowed 判断 domain 是否被“显式授权”。
//
// 典型用途：私有域名（isPrivate=true）不应被 wildcard（"*"）隐式放开，必须显式在 API key domains 中列出，
// 或者由 JWT（单一 domain）自然显式授权。
//
// 规则：
// - API key：只要 DomainSet（或 Domains 列表中除 "*" 外的项）包含该 domain，则视为显式授权。
// - 非 API key（例如未配置 API key 或仅 JWT）：CtxAPIKeyDomains 中出现该 domain 则视为显式授权。
func IsDomainExplicitlyAllowed(c *gin.Context, domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return false
	}

	if info := GetAPIKeyInfo(c); info != nil {
		if info.DomainSet != nil {
			_, ok := info.DomainSet[domain]
			return ok
		}
		for _, d := range info.Domains {
			if d == domain {
				return true
			}
		}
		return false
	}

	// 无 APIKeyInfo 时，直接从 ctx 的 allowed-domains 列表里匹配（JWT 通常是单域名）。
	for _, d := range GetAllowedDomains(c) {
		if strings.ToLower(strings.TrimSpace(d)) == domain {
			return true
		}
	}
	return false
}

// GetAllowedDomains returns the domain list allowed by the current API key.
func GetAllowedDomains(c *gin.Context) []string {
	val, exists := c.Get(CtxAPIKeyDomains)
	if !exists {
		return []string{"*"}
	}
	allowed, ok := val.([]string)
	if !ok {
		return []string{"*"}
	}
	return allowed
}

// HasWildcardAccess returns true if the current API key allows all domains.
func HasWildcardAccess(c *gin.Context) bool {
	if info := GetAPIKeyInfo(c); info != nil {
		if info.Wildcard {
			return true
		}
		// 兼容：若未预计算 Wildcard 字段，则回退扫描 Domains。
		for _, d := range info.Domains {
			if d == "*" {
				return true
			}
		}
		return false
	}

	for _, d := range GetAllowedDomains(c) {
		if d == "*" {
			return true
		}
	}
	return false
}

// GetAPIKeyInfo returns the APIKeyInfo from context, or nil.
func GetAPIKeyInfo(c *gin.Context) *APIKeyInfo {
	val, exists := c.Get(CtxAPIKeyInfo)
	if !exists {
		return nil
	}
	info, ok := val.(*APIKeyInfo)
	if !ok {
		return nil
	}
	return info
}
