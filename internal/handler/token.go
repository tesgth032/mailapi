package handler

import (
	"errors"
	"net/http"
	"strings"

	"mailapi/internal/middleware"
	"mailapi/internal/model"
	"mailapi/internal/store"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func (h *Handler) CreateToken(c *gin.Context) {
	var req model.TokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "address and password required"})
		return
	}

	req.Address = strings.ToLower(strings.TrimSpace(req.Address))

	// Check API key domain access
	domain := middleware.DomainFromAddress(req.Address)
	if !middleware.IsDomainAllowed(c, domain) {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "domain not allowed for this API key"})
		return
	}

	account, err := h.store.GetAccountByAddress(c.Request.Context(), req.Address)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "invalid credentials"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	err = h.withBcryptPermit(c.Request.Context(), func() error {
		return bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(req.Password))
	})
	if err != nil {
		if errors.Is(err, errBcryptBusy) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"code": 503, "message": "server busy, please retry"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"code": 401, "message": "invalid credentials"})
		return
	}

	token, err := h.auth.GenerateToken(account.ID.Hex(), account.Address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, model.TokenResponse{
		Token: token,
		ID:    account.ID.Hex(),
	})
}
