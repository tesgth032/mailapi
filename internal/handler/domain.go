package handler

import (
	"net/http"
	"strings"

	"mailapi/internal/middleware"
	"mailapi/internal/model"

	"github.com/gin-gonic/gin"
)

func (h *Handler) ListDomains(c *gin.Context) {
	entry, err := h.getDomainCache(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to list domains"})
		return
	}
	domains := entry.domains

	// Filter by API key domain access
	if !middleware.HasWildcardAccess(c) {
		var allowedSet map[string]struct{}
		if info := middleware.GetAPIKeyInfo(c); info != nil && !info.Wildcard && info.DomainSet != nil {
			allowedSet = info.DomainSet
		} else {
			allowed := middleware.GetAllowedDomains(c)
			allowedSet = make(map[string]struct{}, len(allowed))
			for _, a := range allowed {
				a = strings.ToLower(a)
				if a == "" || a == "*" {
					continue
				}
				allowedSet[a] = struct{}{}
			}
		}

		filtered := make([]model.Domain, 0, len(domains))
		for _, d := range domains {
			if _, ok := allowedSet[strings.ToLower(d.Domain)]; ok {
				filtered = append(filtered, d)
			}
		}
		domains = filtered
	}

	// 私有域名默认不对 wildcard / 未显式授权的调用方暴露。
	// 规则：isPrivate=true 的域名必须“显式授权”（API key 明确列出，或 JWT 自然单域名授权）才会出现在列表中。
	if len(domains) > 0 {
		filtered := make([]model.Domain, 0, len(domains))
		for _, d := range domains {
			name := strings.ToLower(d.Domain)
			if d.IsPrivate && !middleware.IsDomainExplicitlyAllowed(c, name) {
				continue
			}
			filtered = append(filtered, d)
		}
		domains = filtered
	}

	c.JSON(http.StatusOK, model.HydraCollection{
		Context:    "/contexts/Domain",
		ID:         "/domains",
		Type:       "hydra:Collection",
		TotalItems: int64(len(domains)),
		Member:     domains,
	})
}
