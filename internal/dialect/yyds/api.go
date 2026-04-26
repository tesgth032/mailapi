package yyds

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mailapi/internal/auth"
	"mailapi/internal/cache"
	"mailapi/internal/config"
	"mailapi/internal/handler"
	"mailapi/internal/middleware"
	"mailapi/internal/model"
	"mailapi/internal/prefix"
	"mailapi/internal/storage"
	"mailapi/internal/store"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	ctxAuthKind      = "yyds.auth.kind"
	authKindNone     = ""
	authKindAPIKey   = "api_key"
	authKindToken    = "token"
	defaultListLimit = 50
	maxListLimit     = 200
)

type Config struct {
	Core           *handler.Handler
	Store          store.Interface
	Cache          cache.Interface
	Storage        storage.Interface
	Auth           *auth.Auth
	AccountTTL     time.Duration
	TokenTTL       time.Duration
	APIKeys        map[string]*middleware.APIKeyInfo
	Prefix         *prefix.Generator
	GlobalRPM      int64
	TrustedProxies []string
	AccessLog      config.AccessLogConfig
	Public         config.YYDSDialectConfig
}

type API struct {
	core       *handler.Handler
	store      store.Interface
	cache      cache.Interface
	storage    storage.Interface
	auth       *auth.Auth
	accountTTL time.Duration
	tokenTTL   time.Duration
	apiKeys    map[string]*middleware.APIKeyInfo
	prefix     *prefix.Generator
	globalRPM  int64
	public     config.YYDSDialectConfig
}

type createAccountRequest struct {
	Address            string `json:"address"`
	LocalPart          string `json:"localPart"`
	Domain             string `json:"domain"`
	Subdomain          string `json:"subdomain"`
	AutoDomainStrategy string `json:"autoDomainStrategy"`
}

type tokenRequest struct {
	Address string `json:"address"`
}

type addressRequest struct {
	Address string `json:"address"`
}

type updateMessageRequest struct {
	Seen *bool `json:"seen"`
}

type domainResponse struct {
	ID         string    `json:"id"`
	Domain     string    `json:"domain"`
	IsVerified bool      `json:"isVerified"`
	IsPublic   bool      `json:"isPublic"`
	CreatedAt  time.Time `json:"createdAt,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt,omitempty"`
}

type accountResponse struct {
	ID           string    `json:"id"`
	Address      string    `json:"address"`
	Token        string    `json:"token,omitempty"`
	InboxType    string    `json:"inboxType"`
	Source       string    `json:"source"`
	IsActive     bool      `json:"isActive"`
	MessageCount int64     `json:"messageCount,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type tokenResponse struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	Token   string `json:"token"`
}

type messageListEnvelope struct {
	Messages    []messageSummaryResponse `json:"messages"`
	Total       int64                    `json:"total"`
	UnreadCount int64                    `json:"unreadCount"`
}

type messageSummaryResponse struct {
	ID             string          `json:"id"`
	InboxIDSnake   string          `json:"inbox_id"`
	InboxIDCamel   string          `json:"inboxId"`
	From           model.Address   `json:"from"`
	To             []model.Address `json:"to"`
	Subject        string          `json:"subject"`
	Intro          string          `json:"intro,omitempty"`
	Seen           bool            `json:"seen"`
	HasAttachments bool            `json:"hasAttachments"`
	Size           int64           `json:"size"`
	CreatedAt      time.Time       `json:"createdAt"`
}

type attachmentResponse struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"downloadUrl"`
}

type messageDetailResponse struct {
	ID             string               `json:"id"`
	InboxIDSnake   string               `json:"inbox_id"`
	InboxIDCamel   string               `json:"inboxId"`
	From           model.Address        `json:"from"`
	To             []model.Address      `json:"to"`
	Cc             []model.Address      `json:"cc,omitempty"`
	Bcc            []model.Address      `json:"bcc,omitempty"`
	Subject        string               `json:"subject"`
	Intro          string               `json:"intro,omitempty"`
	Text           string               `json:"text,omitempty"`
	HTML           []string             `json:"html,omitempty"`
	Seen           bool                 `json:"seen"`
	HasAttachments bool                 `json:"hasAttachments"`
	Attachments    []attachmentResponse `json:"attachments,omitempty"`
	Size           int64                `json:"size"`
	CreatedAt      time.Time            `json:"createdAt"`
}

type apiError struct {
	status  int
	code    string
	message string
}

func New(cfg Config) http.Handler {
	pg := cfg.Prefix
	if pg == nil {
		pg = prefix.New()
	}

	rpm := cfg.GlobalRPM
	if rpm == 0 {
		rpm = 100
	} else if rpm < 0 {
		rpm = 0
	}

	public := cfg.Public
	if public.Plans == nil {
		public.Plans = []config.YYDSPlanConfig{}
	}
	if public.Pricing.Packages == nil {
		public.Pricing.Packages = []config.YYDSPricingPackageConfig{}
	}
	if public.Pricing.RateLimits == nil {
		public.Pricing.RateLimits = []config.YYDSPricingRateLimitConfig{}
	}
	if public.Stats.TopDomains == nil {
		public.Stats.TopDomains = []config.YYDSTopDomainConfig{}
	}
	if public.Stats.HourlyActivity == nil {
		public.Stats.HourlyActivity = []config.YYDSHourlyStatConfig{}
	}
	if public.Stats.DailyTrend == nil {
		public.Stats.DailyTrend = []config.YYDSDailyTrendConfig{}
	}

	api := &API{
		core:       cfg.Core,
		store:      cfg.Store,
		cache:      cfg.Cache,
		storage:    cfg.Storage,
		auth:       cfg.Auth,
		accountTTL: cfg.AccountTTL,
		tokenTTL:   cfg.TokenTTL,
		apiKeys:    cfg.APIKeys,
		prefix:     pg,
		globalRPM:  rpm,
		public:     public,
	}

	r := gin.New()
	r.Use(gin.Recovery())
	if len(cfg.TrustedProxies) > 0 {
		_ = r.SetTrustedProxies(cfg.TrustedProxies)
	}
	if cfg.AccessLog.Enabled {
		r.Use(middleware.AccessLog(cfg.AccessLog.SampleEvery, cfg.AccessLog.SlowThreshold, cfg.AccessLog.ErrorsOnly))
	}
	r.Use(api.globalRateLimit())
	r.Use(api.authMiddleware())
	r.Use(api.apiKeyRateLimit())

	r.GET("/v1/domains", api.listDomains)
	r.GET("/v1/plans", api.listPlans)
	r.GET("/v1/pricing", api.getPricing)
	r.GET("/v1/domain-reward/config", api.getDomainRewardConfig)
	r.GET("/v1/stats", api.getStats)
	r.GET("/v1/llms.txt", api.getLLMsText)

	r.POST("/v1/accounts", api.requireCreateAccountAuth(), api.createAccount)
	r.POST("/v1/accounts/wildcard", api.requireCreateAccountAuth(), api.createWildcardAccount)
	r.POST("/v1/token", api.createToken)
	r.GET("/v1/accounts/me", api.requireTokenAuth(), api.getCurrentAccount)
	r.GET("/v1/accounts/:id", api.requireAnyAuth(), api.getAccountByID)
	r.DELETE("/v1/accounts/:id", api.requireAnyAuth(), api.deleteAccount)

	r.GET("/v1/messages", api.requireAnyAuth(), api.listMessages)
	r.POST("/v1/messages/mark-read", api.requireAnyAuth(), api.markMessagesRead)
	r.GET("/v1/messages/:id", api.requireAnyAuth(), api.getMessage)
	r.PATCH("/v1/messages/:id", api.requireAnyAuth(), api.updateMessage)
	r.DELETE("/v1/messages/:id", api.requireAnyAuth(), api.deleteMessage)
	r.GET("/v1/sources/:id", api.requireAnyAuth(), api.getMessageSource)
	r.GET("/v1/messages/:id/attachments/:attachmentId", api.requireAnyAuth(), api.downloadAttachment)

	return r
}

func (a *API) globalRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a.cache == nil || a.globalRPM <= 0 {
			c.Next()
			return
		}

		allowed, err := a.cache.CheckRateLimit(c.Request.Context(), c.ClientIP(), a.globalRPM, time.Minute)
		if err != nil || allowed {
			c.Next()
			return
		}

		c.Header("Retry-After", "60")
		abortError(c, http.StatusTooManyRequests, "rate_limit_exceeded", "Rate limit exceeded")
	}
}

func (a *API) apiKeyRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a.cache == nil {
			c.Next()
			return
		}

		info := middleware.GetAPIKeyInfo(c)
		if info == nil || info.RPMLimit <= 0 {
			c.Next()
			return
		}

		apiKey := c.GetString(middleware.CtxAPIKey)
		allowed, err := a.cache.CheckKeyRateLimit(c.Request.Context(), apiKey, info.RPMLimit, time.Minute)
		if err != nil || allowed {
			c.Next()
			return
		}

		c.Header("Retry-After", "60")
		abortError(c, http.StatusTooManyRequests, "api_key_rate_limit_exceeded", "API key rate limit exceeded")
	}
}

func (a *API) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(a.apiKeys) == 0 {
			c.Set(middleware.CtxAPIKeyDomains, []string{"*"})
		} else {
			c.Set(middleware.CtxAPIKeyDomains, []string{})
		}
		c.Set(ctxAuthKind, authKindNone)

		if key := strings.TrimSpace(c.GetHeader("X-API-Key")); key != "" {
			info, ok := a.apiKeys[key]
			if !ok {
				abortError(c, http.StatusUnauthorized, "invalid_api_key", "Invalid API key")
				return
			}
			a.applyAPIKeyContext(c, key, info)
			c.Next()
			return
		}

		token, ok := bearerTokenFromAuthorization(c.GetHeader("Authorization"))
		if !ok || token == "" {
			c.Next()
			return
		}

		if info, ok := a.apiKeys[token]; ok {
			a.applyAPIKeyContext(c, token, info)
			c.Next()
			return
		}

		if a.auth == nil {
			abortError(c, http.StatusUnauthorized, "invalid_or_expired_token", "Invalid or expired token")
			return
		}

		claims, err := a.auth.ValidateToken(token)
		if err != nil {
			abortError(c, http.StatusUnauthorized, "invalid_or_expired_token", "Invalid or expired token")
			return
		}

		c.Set(ctxAuthKind, authKindToken)
		c.Set("accountId", claims.AccountID)
		c.Set("address", strings.ToLower(strings.TrimSpace(claims.Address)))
		if domain := middleware.DomainFromAddress(claims.Address); domain != "" {
			c.Set(middleware.CtxAPIKeyDomains, []string{domain})
		}
		c.Next()
	}
}

func (a *API) applyAPIKeyContext(c *gin.Context, key string, info *middleware.APIKeyInfo) {
	c.Set(ctxAuthKind, authKindAPIKey)
	c.Set(middleware.CtxAPIKey, key)
	c.Set(middleware.CtxAPIKeyName, info.Name)
	c.Set(middleware.CtxAPIKeyInfo, info)
	if info != nil && len(info.Domains) > 0 {
		c.Set(middleware.CtxAPIKeyDomains, info.Domains)
	} else {
		c.Set(middleware.CtxAPIKeyDomains, []string{})
	}
}

func (a *API) requireCreateAccountAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if len(a.apiKeys) == 0 {
			c.Next()
			return
		}
		if a.authKind(c) != authKindNone {
			c.Next()
			return
		}
		abortError(c, http.StatusUnauthorized, "authorization_required_create", "Authorization required (API key or temp token)")
	}
}

func (a *API) requireAnyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a.authKind(c) != authKindNone {
			c.Next()
			return
		}
		abortError(c, http.StatusUnauthorized, "authorization_required_any", "Authorization required (JWT, API key, or temp token)")
	}
}

func (a *API) requireTokenAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a.authKind(c) == authKindToken {
			c.Next()
			return
		}
		abortError(c, http.StatusUnauthorized, "authorization_required_temp_token", "Temp token required")
	}
}

func (a *API) listDomains(c *gin.Context) {
	domains, err := a.visibleDomains(c.Request.Context(), c)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to list domains")
		return
	}

	resp := make([]domainResponse, 0, len(domains))
	for _, d := range domains {
		resp = append(resp, domainResponse{
			ID:         objectIDString(d.ID),
			Domain:     strings.ToLower(d.Domain),
			IsVerified: d.IsActive,
			IsPublic:   !d.IsPrivate,
			CreatedAt:  d.CreatedAt,
			UpdatedAt:  d.UpdatedAt,
		})
	}

	writeSuccess(c, http.StatusOK, resp)
}

func (a *API) listPlans(c *gin.Context) {
	writeSuccess(c, http.StatusOK, a.public.Plans)
}

func (a *API) getPricing(c *gin.Context) {
	writeSuccess(c, http.StatusOK, a.public.Pricing)
}

func (a *API) getDomainRewardConfig(c *gin.Context) {
	writeSuccess(c, http.StatusOK, a.public.DomainReward)
}

func (a *API) getStats(c *gin.Context) {
	stats := a.public.Stats
	if stats.TotalDomains == 0 && stats.VerifiedDomains == 0 && stats.PublicDomains == 0 {
		if domains, err := a.store.ListDomains(c.Request.Context()); err == nil {
			stats.TotalDomains = int64(len(domains))
			stats.VerifiedDomains = int64(len(domains))
			for _, d := range domains {
				if !d.IsPrivate {
					stats.PublicDomains++
				}
			}
		}
	}
	writeSuccess(c, http.StatusOK, stats)
}

func (a *API) createAccount(c *gin.Context) {
	a.createAccountWithMode(c, false)
}

func (a *API) createWildcardAccount(c *gin.Context) {
	a.createAccountWithMode(c, true)
}

func (a *API) createAccountWithMode(c *gin.Context, forceWildcard bool) {
	var req createAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, "invalid_request_body", "Invalid request body")
		return
	}

	address, autoGenerated, errResp := a.resolveCreateAddress(c, req, forceWildcard)
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	password, err := randomPassword()
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to create inbox")
		return
	}

	account := &model.Account{
		Address:  address,
		Password: password,
	}

	for attempt := 0; attempt < 5; attempt++ {
		err = a.store.CreateAccount(c.Request.Context(), account)
		if err == nil {
			break
		}
		if !autoGenerated || err != store.ErrDuplicateKey {
			break
		}

		domain := middleware.DomainFromAddress(account.Address)
		account.Address = strings.ToLower(a.prefix.GenerateForDomain(domain))
	}
	if err != nil {
		switch {
		case err == store.ErrDuplicateKey:
			abortError(c, http.StatusConflict, "address_already_exists", "Address already exists")
		default:
			abortError(c, http.StatusInternalServerError, "internal_error", "Failed to create inbox")
		}
		return
	}

	if a.cache != nil {
		_ = a.cache.SetAddress(c.Request.Context(), account.Address, a.accountTTL)
	}

	token, err := a.auth.GenerateToken(account.ID.Hex(), account.Address)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to create inbox token")
		return
	}

	writeSuccess(c, http.StatusCreated, a.toAccountResponse(account, token, 0))
}

func (a *API) createToken(c *gin.Context) {
	var req tokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortError(c, http.StatusBadRequest, "invalid_request_body", "Invalid request body")
		return
	}

	account, errResp := a.lookupAccountByAddress(c, req.Address, false)
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	token, err := a.auth.GenerateToken(account.ID.Hex(), account.Address)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to refresh token")
		return
	}

	writeSuccess(c, http.StatusOK, tokenResponse{
		ID:      account.ID.Hex(),
		Address: account.Address,
		Token:   token,
	})
}

func (a *API) getCurrentAccount(c *gin.Context) {
	account, errResp := a.currentTokenAccount(c)
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	count, err := a.store.CountMessagesByAccount(c.Request.Context(), account.ID)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to load inbox")
		return
	}

	writeSuccess(c, http.StatusOK, a.toAccountResponse(account, "", count))
}

func (a *API) getAccountByID(c *gin.Context) {
	account, errResp := a.accountByIDAuthorized(c, c.Param("id"))
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	count, err := a.store.CountMessagesByAccount(c.Request.Context(), account.ID)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to load inbox")
		return
	}

	writeSuccess(c, http.StatusOK, a.toAccountResponse(account, "", count))
}

func (a *API) deleteAccount(c *gin.Context) {
	account, errResp := a.accountByIDAuthorized(c, c.Param("id"))
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	c.Set("accountId", account.ID.Hex())
	c.Set("address", account.Address)

	if a.core != nil {
		a.core.DeleteAccount(c)
		return
	}

	if a.cache != nil {
		_ = a.cache.RemoveAddress(c.Request.Context(), account.Address)
	}
	if err := a.store.DeleteAccount(c.Request.Context(), account.ID.Hex()); err != nil {
		if err == store.ErrNotFound {
			abortError(c, http.StatusNotFound, "account_not_found", "Inbox not found")
			return
		}
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to delete inbox")
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *API) listMessages(c *gin.Context) {
	account, errResp := a.messageListAccount(c)
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	limit := parseLimit(c.Query("limit"))

	messages, total, err := a.store.ListMessages(c.Request.Context(), account.ID, 1, limit)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to list messages")
		return
	}

	unread := false
	_, unreadCount, err := a.store.ListMessagesFiltered(c.Request.Context(), account.ID, 1, 1, &unread)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to list messages")
		return
	}

	resp := make([]messageSummaryResponse, 0, len(messages))
	for _, msg := range messages {
		resp = append(resp, toMessageSummary(msg))
	}

	writeSuccess(c, http.StatusOK, messageListEnvelope{
		Messages:    resp,
		Total:       total,
		UnreadCount: unreadCount,
	})
}

func (a *API) markMessagesRead(c *gin.Context) {
	account, errResp := a.accountFromAddressBodyOrToken(c)
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	seen := true
	updated, err := a.store.BulkUpdateMessageFlagsByAccount(c.Request.Context(), account.ID, &seen, nil)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to update messages")
		return
	}

	total, err := a.store.CountMessagesByAccount(c.Request.Context(), account.ID)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to update messages")
		return
	}

	alreadySeen := total - updated
	if alreadySeen < 0 {
		alreadySeen = 0
	}

	writeSuccess(c, http.StatusOK, gin.H{
		"updated":     updated,
		"alreadySeen": alreadySeen,
		"total":       total,
	})
}

func (a *API) getMessage(c *gin.Context) {
	msg, errResp := a.messageByIDAuthorized(c, c.Param("id"))
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	writeSuccess(c, http.StatusOK, toMessageDetail(msg))
}

func (a *API) updateMessage(c *gin.Context) {
	msg, errResp := a.messageByIDAuthorized(c, c.Param("id"))
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	var req updateMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Seen == nil {
		abortError(c, http.StatusBadRequest, "invalid_request_body", "Invalid request body")
		return
	}

	if err := a.store.UpdateMessageFlags(c.Request.Context(), msg.ID.Hex(), req.Seen, nil); err != nil {
		switch err {
		case store.ErrNotFound, store.ErrInvalidID:
			abortError(c, http.StatusNotFound, "message_not_found", "Message not found")
		default:
			abortError(c, http.StatusInternalServerError, "internal_error", "Failed to update message")
		}
		return
	}

	writeSuccess(c, http.StatusOK, gin.H{
		"id":   msg.ID.Hex(),
		"seen": *req.Seen,
	})
}

func (a *API) deleteMessage(c *gin.Context) {
	msg, errResp := a.messageMetaByIDAuthorized(c, c.Param("id"))
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	c.Set("accountId", msg.AccountID.Hex())
	if a.core != nil {
		a.core.DeleteMessage(c)
		return
	}

	if err := a.store.DeleteMessage(c.Request.Context(), msg.ID.Hex()); err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to delete message")
		return
	}
	_ = a.store.UpdateAccountUsed(c.Request.Context(), msg.AccountID, -msg.Size)
	c.Status(http.StatusNoContent)
}

func (a *API) getMessageSource(c *gin.Context) {
	msg, errResp := a.messageRawByIDAuthorized(c, c.Param("id"))
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	raw := msg.RawMessage
	if len(raw) == 0 {
		if a.storage == nil {
			abortError(c, http.StatusNotFound, "source_not_available", "Raw source not available")
			return
		}

		obj, _, _, err := a.storage.OpenRawMessage(c.Request.Context(), msg.ID.Hex())
		if err != nil {
			abortError(c, http.StatusNotFound, "source_not_available", "Raw source not available")
			return
		}
		defer obj.Close()

		raw, err = io.ReadAll(obj)
		if err != nil {
			abortError(c, http.StatusInternalServerError, "internal_error", "Failed to load raw source")
			return
		}
	}

	writeSuccess(c, http.StatusOK, gin.H{
		"id":  msg.ID.Hex(),
		"raw": string(raw),
	})
}

func (a *API) downloadAttachment(c *gin.Context) {
	msg, errResp := a.messageMetaByIDAuthorized(c, c.Param("id"))
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}

	c.Set("accountId", msg.AccountID.Hex())
	if a.core != nil {
		a.core.DownloadAttachment(c)
		return
	}

	if a.storage == nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Attachment storage not configured")
		return
	}

	attachmentID := c.Param("attachmentId")
	var att *model.Attachment
	for i := range msg.Attachments {
		if msg.Attachments[i].ID == attachmentID {
			att = &msg.Attachments[i]
			break
		}
	}
	if att == nil {
		abortError(c, http.StatusNotFound, "attachment_not_found", "Attachment not found")
		return
	}

	obj, contentType, size, err := a.storage.Open(c.Request.Context(), msg.ID.Hex(), attachmentID, att.Filename)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to download attachment")
		return
	}
	defer obj.Close()

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", att.Filename))
	c.DataFromReader(http.StatusOK, size, contentType, obj, nil)
}

func (a *API) getLLMsText(c *gin.Context) {
	baseURL := a.baseURL(c)
	text := fmt.Sprintf(`# MailAPI yyds dialect
> Public temporary-inbox integration summary for developers and AI assistants.
> Base URL: %s

## Public Scope
This llms.txt documents the public yyds-compatible surface implemented by MailAPI:
- Domains
- Temporary Email
- Messages
- Public metadata endpoints

Public query endpoints:
- GET /v1/domains
- GET /v1/plans
- GET /v1/pricing
- GET /v1/domain-reward/config
- GET /v1/stats
- GET /v1/llms.txt

## Authentication
- API Key: X-API-Key: <configured key>
- Temp Token: Authorization: Bearer <temp_token>

## Temporary Email
- POST   /v1/accounts
- POST   /v1/accounts/wildcard
- POST   /v1/token
- GET    /v1/accounts/me
- GET    /v1/accounts/{id}
- DELETE /v1/accounts/{id}

Notes:
- POST /v1/accounts accepts localPart, address, domain, and subdomain.
- localPart is preferred; address remains accepted for compatibility.
- /v1/accounts/wildcard only supports child domains that are already configured as receive domains in MailAPI.
- POST /v1/token refreshes a temp token by address. Unauthenticated access is only allowed for public domains.

## Messages
- GET    /v1/messages
- POST   /v1/messages/mark-read
- GET    /v1/messages/{id}
- PATCH  /v1/messages/{id}
- DELETE /v1/messages/{id}
- GET    /v1/sources/{id}

## Error Handling
All errors follow the same envelope:
{ "success": false, "error": "...", "errorCode": "..." }

429 responses include Retry-After.

## Intentionally Excluded
YYDS Mail dashboard-only features such as signed-in console routes, billing, webhook management, and DNS automation are not part of this MailAPI dialect.
`, baseURL)

	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(text))
}

func (a *API) visibleDomains(ctx context.Context, c *gin.Context) ([]model.Domain, error) {
	domains, err := a.store.ListDomains(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]model.Domain, 0, len(domains))
	for _, d := range domains {
		name := strings.ToLower(strings.TrimSpace(d.Domain))
		if name == "" {
			continue
		}

		switch a.authKind(c) {
		case authKindAPIKey, authKindToken:
			if !middleware.IsDomainAllowed(c, name) {
				continue
			}
			if d.IsPrivate && !middleware.IsDomainExplicitlyAllowed(c, name) {
				continue
			}
		default:
			if d.IsPrivate {
				continue
			}
		}

		out = append(out, d)
	}

	return out, nil
}

func (a *API) resolveCreateAddress(c *gin.Context, req createAccountRequest, forceWildcard bool) (string, bool, *apiError) {
	rawAddress := strings.TrimSpace(req.Address)
	localPart := strings.TrimSpace(req.LocalPart)
	requestedDomain := strings.ToLower(strings.TrimSpace(req.Domain))
	subdomain := strings.ToLower(strings.TrimSpace(req.Subdomain))

	if strings.Contains(rawAddress, "@") {
		address := strings.ToLower(rawAddress)
		if _, errResp := a.checkDomainAccessibleForAddress(c, address, true); errResp != nil {
			return "", false, errResp
		}
		return address, false, nil
	}

	if localPart == "" && rawAddress != "" {
		localPart = rawAddress
	}

	if subdomain != "" && requestedDomain == "" {
		return "", false, &apiError{status: http.StatusBadRequest, code: "domain_required", message: "domain is required when subdomain is provided"}
	}

	if forceWildcard && subdomain == "" {
		return "", false, &apiError{status: http.StatusBadRequest, code: "subdomain_required_for_mailapi_wildcard", message: "mailapi requires an explicit preconfigured child domain for wildcard inbox creation"}
	}

	targetDomain := requestedDomain
	if targetDomain == "" {
		domain, errResp := a.defaultDomainForCreate(c)
		if errResp != nil {
			return "", false, errResp
		}
		targetDomain = domain
	}
	if subdomain != "" {
		targetDomain = subdomain + "." + targetDomain
	}

	if _, errResp := a.ensureDomainAccessible(c, targetDomain, true); errResp != nil {
		return "", false, errResp
	}

	autoGenerated := false
	if localPart == "" {
		localPart = a.prefix.Generate()
		autoGenerated = true
	}

	return strings.ToLower(localPart + "@" + targetDomain), autoGenerated, nil
}

func (a *API) defaultDomainForCreate(c *gin.Context) (string, *apiError) {
	domains, err := a.visibleDomains(c.Request.Context(), c)
	if err != nil {
		return "", &apiError{status: http.StatusInternalServerError, code: "internal_error", message: "Failed to load domains"}
	}
	if len(domains) == 0 {
		return "", &apiError{status: http.StatusBadRequest, code: "domain_not_available", message: "No domain available for inbox creation"}
	}
	return strings.ToLower(domains[0].Domain), nil
}

func (a *API) ensureDomainAccessible(c *gin.Context, domain string, allowAnonymousPublic bool) (*model.Domain, *apiError) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, &apiError{status: http.StatusBadRequest, code: "domain_required", message: "domain is required"}
	}

	di, err := a.store.GetDomainByName(c.Request.Context(), domain)
	if err != nil {
		if err == store.ErrNotFound {
			return nil, &apiError{status: http.StatusBadRequest, code: "domain_not_available", message: "Domain not available"}
		}
		return nil, &apiError{status: http.StatusInternalServerError, code: "internal_error", message: "Failed to load domain"}
	}

	switch a.authKind(c) {
	case authKindAPIKey, authKindToken:
		if !middleware.IsDomainAllowed(c, domain) {
			return nil, &apiError{status: http.StatusForbidden, code: "domain_not_allowed", message: "Domain not allowed for this API key"}
		}
		if di.IsPrivate && !middleware.IsDomainExplicitlyAllowed(c, domain) {
			return nil, &apiError{status: http.StatusForbidden, code: "private_domain_requires_explicit_authorization", message: "Private domain requires explicit authorization"}
		}
	default:
		if di.IsPrivate || !allowAnonymousPublic {
			return nil, &apiError{status: http.StatusForbidden, code: "domain_not_public", message: "Domain is not available for anonymous access"}
		}
	}

	return di, nil
}

func (a *API) checkDomainAccessibleForAddress(c *gin.Context, address string, allowAnonymousPublic bool) (*model.Domain, *apiError) {
	address = strings.ToLower(strings.TrimSpace(address))
	domain := middleware.DomainFromAddress(address)
	if domain == "" {
		return nil, &apiError{status: http.StatusBadRequest, code: "invalid_address", message: "Invalid address"}
	}
	return a.ensureDomainAccessible(c, domain, allowAnonymousPublic)
}

func (a *API) lookupAccountByAddress(c *gin.Context, address string, authRequired bool) (*model.Account, *apiError) {
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" {
		return nil, &apiError{status: http.StatusBadRequest, code: "address_required", message: "Address is required"}
	}
	if _, errResp := a.checkDomainAccessibleForAddress(c, address, !authRequired); errResp != nil {
		return nil, errResp
	}

	account, err := a.store.GetAccountByAddress(c.Request.Context(), address)
	if err != nil {
		if err == store.ErrNotFound {
			return nil, &apiError{status: http.StatusNotFound, code: "account_not_found", message: "Inbox not found"}
		}
		return nil, &apiError{status: http.StatusInternalServerError, code: "internal_error", message: "Failed to load inbox"}
	}
	return account, nil
}

func (a *API) currentTokenAccount(c *gin.Context) (*model.Account, *apiError) {
	accountID := c.GetString("accountId")
	if accountID == "" {
		return nil, &apiError{status: http.StatusUnauthorized, code: "authorization_required_temp_token", message: "Temp token required"}
	}

	account, err := a.store.GetAccount(c.Request.Context(), accountID)
	if err != nil {
		switch err {
		case store.ErrNotFound, store.ErrInvalidID:
			return nil, &apiError{status: http.StatusNotFound, code: "account_not_found", message: "Inbox not found"}
		default:
			return nil, &apiError{status: http.StatusInternalServerError, code: "internal_error", message: "Failed to load inbox"}
		}
	}
	return account, nil
}

func (a *API) accountByIDAuthorized(c *gin.Context, id string) (*model.Account, *apiError) {
	account, err := a.store.GetAccount(c.Request.Context(), id)
	if err != nil {
		switch err {
		case store.ErrNotFound, store.ErrInvalidID:
			return nil, &apiError{status: http.StatusNotFound, code: "account_not_found", message: "Inbox not found"}
		default:
			return nil, &apiError{status: http.StatusInternalServerError, code: "internal_error", message: "Failed to load inbox"}
		}
	}

	if a.authKind(c) == authKindToken {
		if account.ID.Hex() != c.GetString("accountId") {
			return nil, &apiError{status: http.StatusForbidden, code: "forbidden", message: "Access denied"}
		}
		return account, nil
	}

	if _, errResp := a.checkDomainAccessibleForAddress(c, account.Address, false); errResp != nil {
		return nil, errResp
	}
	return account, nil
}

func (a *API) messageListAccount(c *gin.Context) (*model.Account, *apiError) {
	if a.authKind(c) == authKindToken {
		account, errResp := a.currentTokenAccount(c)
		if errResp != nil {
			return nil, errResp
		}

		if addr := strings.ToLower(strings.TrimSpace(c.Query("address"))); addr != "" && addr != strings.ToLower(account.Address) {
			return nil, &apiError{status: http.StatusForbidden, code: "forbidden", message: "Address does not match temp token"}
		}
		return account, nil
	}

	return a.lookupAccountByAddress(c, c.Query("address"), true)
}

func (a *API) accountFromAddressBodyOrToken(c *gin.Context) (*model.Account, *apiError) {
	if a.authKind(c) == authKindToken {
		account, errResp := a.currentTokenAccount(c)
		if errResp != nil {
			return nil, errResp
		}

		var req addressRequest
		if c.Request.Body != nil {
			_ = c.ShouldBindJSON(&req)
		}
		if addr := strings.ToLower(strings.TrimSpace(req.Address)); addr != "" && addr != strings.ToLower(account.Address) {
			return nil, &apiError{status: http.StatusForbidden, code: "forbidden", message: "Address does not match temp token"}
		}
		return account, nil
	}

	var req addressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return nil, &apiError{status: http.StatusBadRequest, code: "invalid_request_body", message: "Invalid request body"}
	}
	return a.lookupAccountByAddress(c, req.Address, true)
}

func (a *API) messageByIDAuthorized(c *gin.Context, id string) (*model.Message, *apiError) {
	msg, err := a.store.GetMessage(c.Request.Context(), id)
	if err != nil {
		switch err {
		case store.ErrNotFound, store.ErrInvalidID:
			return nil, &apiError{status: http.StatusNotFound, code: "message_not_found", message: "Message not found"}
		default:
			return nil, &apiError{status: http.StatusInternalServerError, code: "internal_error", message: "Failed to load message"}
		}
	}

	if errResp := a.authorizeMessageAccount(c, msg.AccountID); errResp != nil {
		return nil, errResp
	}
	return msg, nil
}

func (a *API) messageMetaByIDAuthorized(c *gin.Context, id string) (*model.Message, *apiError) {
	msg, err := a.store.GetMessageMeta(c.Request.Context(), id)
	if err != nil {
		switch err {
		case store.ErrNotFound, store.ErrInvalidID:
			return nil, &apiError{status: http.StatusNotFound, code: "message_not_found", message: "Message not found"}
		default:
			return nil, &apiError{status: http.StatusInternalServerError, code: "internal_error", message: "Failed to load message"}
		}
	}

	if errResp := a.authorizeMessageAccount(c, msg.AccountID); errResp != nil {
		return nil, errResp
	}
	return msg, nil
}

func (a *API) messageRawByIDAuthorized(c *gin.Context, id string) (*model.Message, *apiError) {
	msg, err := a.store.GetMessageRaw(c.Request.Context(), id)
	if err != nil {
		switch err {
		case store.ErrNotFound, store.ErrInvalidID:
			return nil, &apiError{status: http.StatusNotFound, code: "message_not_found", message: "Message not found"}
		default:
			return nil, &apiError{status: http.StatusInternalServerError, code: "internal_error", message: "Failed to load message"}
		}
	}

	if errResp := a.authorizeMessageAccount(c, msg.AccountID); errResp != nil {
		return nil, errResp
	}
	return msg, nil
}

func (a *API) authorizeMessageAccount(c *gin.Context, accountID bson.ObjectID) *apiError {
	if a.authKind(c) == authKindToken {
		if accountID.Hex() != c.GetString("accountId") {
			return &apiError{status: http.StatusForbidden, code: "forbidden", message: "Access denied"}
		}
		return nil
	}

	account, err := a.store.GetAccount(c.Request.Context(), accountID.Hex())
	if err != nil {
		switch err {
		case store.ErrNotFound, store.ErrInvalidID:
			return &apiError{status: http.StatusNotFound, code: "account_not_found", message: "Inbox not found"}
		default:
			return &apiError{status: http.StatusInternalServerError, code: "internal_error", message: "Failed to load inbox"}
		}
	}

	if _, errResp := a.checkDomainAccessibleForAddress(c, account.Address, false); errResp != nil {
		return errResp
	}
	return nil
}

func (a *API) toAccountResponse(account *model.Account, token string, messageCount int64) accountResponse {
	createdAt := account.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	return accountResponse{
		ID:           account.ID.Hex(),
		Address:      strings.ToLower(account.Address),
		Token:        token,
		InboxType:    "temp",
		Source:       "api",
		IsActive:     !account.IsDeleted,
		MessageCount: messageCount,
		CreatedAt:    createdAt,
		ExpiresAt:    createdAt.Add(a.accountTTL),
	}
}

func toMessageSummary(msg model.Message) messageSummaryResponse {
	inboxID := msg.AccountID.Hex()
	return messageSummaryResponse{
		ID:             msg.ID.Hex(),
		InboxIDSnake:   inboxID,
		InboxIDCamel:   inboxID,
		From:           msg.From,
		To:             msg.To,
		Subject:        msg.Subject,
		Intro:          msg.Intro,
		Seen:           msg.Seen,
		HasAttachments: msg.HasAttachments,
		Size:           msg.Size,
		CreatedAt:      msg.CreatedAt,
	}
}

func toMessageDetail(msg *model.Message) messageDetailResponse {
	inboxID := msg.AccountID.Hex()
	attachments := make([]attachmentResponse, 0, len(msg.Attachments))
	for _, att := range msg.Attachments {
		attachments = append(attachments, attachmentResponse{
			ID:          att.ID,
			Filename:    att.Filename,
			ContentType: att.ContentType,
			Size:        att.Size,
			DownloadURL: fmt.Sprintf("/v1/messages/%s/attachments/%s", msg.ID.Hex(), att.ID),
		})
	}
	return messageDetailResponse{
		ID:             msg.ID.Hex(),
		InboxIDSnake:   inboxID,
		InboxIDCamel:   inboxID,
		From:           msg.From,
		To:             msg.To,
		Cc:             msg.Cc,
		Bcc:            msg.Bcc,
		Subject:        msg.Subject,
		Intro:          msg.Intro,
		Text:           msg.Text,
		HTML:           msg.HTML,
		Seen:           msg.Seen,
		HasAttachments: msg.HasAttachments,
		Attachments:    attachments,
		Size:           msg.Size,
		CreatedAt:      msg.CreatedAt,
	}
}

func (a *API) baseURL(c *gin.Context) string {
	if s := strings.TrimSpace(a.public.PublicBaseURL); s != "" {
		return strings.TrimRight(s, "/")
	}

	scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	return fmt.Sprintf("%s://%s/v1", scheme, c.Request.Host)
}

func (a *API) authKind(c *gin.Context) string {
	v, _ := c.Get(ctxAuthKind)
	s, _ := v.(string)
	return s
}

func parseLimit(raw string) int {
	if raw == "" {
		return defaultListLimit
	}

	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &n); err != nil || n <= 0 {
		return defaultListLimit
	}
	if n > maxListLimit {
		return maxListLimit
	}
	return n
}

func bearerTokenFromAuthorization(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if len(header) < 7 || !strings.EqualFold(header[:6], "Bearer") {
		return "", false
	}
	if header[6] != ' ' && header[6] != '\t' {
		return "", false
	}
	return strings.TrimSpace(header[6:]), true
}

func randomPassword() (string, error) {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func objectIDString(id bson.ObjectID) string {
	if id == (bson.ObjectID{}) {
		return ""
	}
	return id.Hex()
}

func writeSuccess(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{
		"success": true,
		"data":    data,
	})
}

func abortError(c *gin.Context, status int, errorCode, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"success":   false,
		"error":     message,
		"errorCode": errorCode,
	})
}
