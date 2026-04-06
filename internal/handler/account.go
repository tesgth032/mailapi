package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"mailapi/internal/middleware"
	"mailapi/internal/model"
	"mailapi/internal/store"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func (h *Handler) CreateAccount(c *gin.Context) {
	var req model.CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request: password (min 6 chars) required, plus address or domain"})
		return
	}

	// Auto-generate prefix if address not provided
	if req.Address == "" {
		if req.Domain == "" {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "either address or domain is required"})
			return
		}
		req.Domain = strings.ToLower(req.Domain)
		req.Address = h.prefix.GenerateForDomain(req.Domain)
	}

	// Validate domain
	parts := strings.SplitN(req.Address, "@", 2)
	if len(parts) != 2 || len(parts[0]) < 3 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid email address (username must be >= 3 chars)"})
		return
	}
	domain := strings.ToLower(parts[1])

	// Check API key domain access
	if !middleware.IsDomainAllowed(c, domain) {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "domain not allowed for this API key"})
		return
	}

	// Check per-key-per-domain rate limit
	if !middleware.CheckDomainRateLimit(c, h.cache, domain) {
		c.JSON(http.StatusTooManyRequests, gin.H{"code": 429, "message": "domain rate limit exceeded for this API key"})
		return
	}

	di, err := h.getDomainInfo(c.Request.Context(), domain)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "domain not available"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}
	// 私有域名必须显式授权（不能仅靠 wildcard "*" 放开）。
	if di.IsPrivate && !middleware.IsDomainExplicitlyAllowed(c, domain) {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "private domain requires explicit API key authorization"})
		return
	}

	// Hash password
	var hashed []byte
	if err := h.withBcryptPermit(c.Request.Context(), func() error {
		var err error
		hashed, err = bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		return err
	}); err != nil {
		if errors.Is(err, errBcryptBusy) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "server busy, please retry"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	account := &model.Account{
		Address:  strings.ToLower(req.Address),
		Password: string(hashed),
	}

	if err := h.store.CreateAccount(c.Request.Context(), account); err != nil {
		if errors.Is(err, store.ErrDuplicateKey) {
			// If auto-generated, retry with a new prefix
			if req.Domain != "" {
				for range 5 {
					account.Address = strings.ToLower(h.prefix.GenerateForDomain(req.Domain))
					if err := h.store.CreateAccount(c.Request.Context(), account); err == nil {
						goto created
					} else if !errors.Is(err, store.ErrDuplicateKey) {
						// 非重复冲突类错误不应被误报为“地址已占用”
						c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to create account"})
						return
					}
				}
			}
			c.JSON(http.StatusConflict, gin.H{"code": 409, "message": "address already in use"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to create account"})
		return
	}

created:
	// Cache address in Redis for SMTP RCPT TO fast validation
	if err := h.cache.SetAddress(c.Request.Context(), account.Address, h.accountTTL); err != nil {
		// Non-fatal, log but continue
	}

	c.JSON(http.StatusCreated, account)
}

// RandomAddress generates random human-like email addresses for a given domain.
func (h *Handler) RandomAddress(c *gin.Context) {
	domain := strings.ToLower(c.Query("domain"))
	if domain == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "domain query parameter is required"})
		return
	}

	// Check domain access
	if !middleware.IsDomainAllowed(c, domain) {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "domain not allowed for this API key"})
		return
	}

	// Verify domain exists
	di, err := h.getDomainInfo(c.Request.Context(), domain)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "domain not available"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}
	if di.IsPrivate && !middleware.IsDomainExplicitlyAllowed(c, domain) {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "private domain requires explicit API key authorization"})
		return
	}

	count := 5
	if s := c.Query("count"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			count = n
		}
		if count > 50 {
			count = 50
		}
	}

	prefixes := h.prefix.GenerateN(count)
	addresses := make([]string, len(prefixes))
	for i, p := range prefixes {
		addresses[i] = p + "@" + domain
	}

	c.JSON(http.StatusOK, gin.H{"addresses": addresses})
}

func (h *Handler) GetAccount(c *gin.Context) {
	id := c.Param("id")
	callerID := c.GetString("accountId")

	if id != callerID {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "access denied"})
		return
	}

	account, err := h.store.GetAccount(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidID) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "account not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	c.JSON(http.StatusOK, account)
}

func (h *Handler) GetMe(c *gin.Context) {
	accountID := c.GetString("accountId")

	account, err := h.store.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidID) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "account not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	c.JSON(http.StatusOK, account)
}

func (h *Handler) DeleteAccount(c *gin.Context) {
	id := c.Param("id")
	callerID := c.GetString("accountId")

	if id != callerID {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "access denied"})
		return
	}

	account, err := h.store.GetAccount(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidID) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "account not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	// Remove address from Redis cache
	_ = h.cache.RemoveAddress(c.Request.Context(), account.Address)

	// Delete all messages and their attachments
	messages, err := h.store.HardDeleteMessagesByAccount(c.Request.Context(), account.ID)
	if err == nil {
		for _, msg := range messages {
			// rawMessage/attachments 都可能存放在对象存储中，统一按 message 前缀清理。
			h.tryAsyncStorageCleanup(msg.ID.Hex())
		}
	}

	// Delete the account
	if err := h.store.DeleteAccount(c.Request.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "account not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to delete account"})
		return
	}

	c.Status(http.StatusNoContent)
}
