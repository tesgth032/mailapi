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

	c.JSON(http.StatusOK, model.HydraCollection{
		Context:    "/contexts/Domain",
		ID:         "/domains",
		Type:       "hydra:Collection",
		TotalItems: int64(len(domains)),
		Member:     domains,
	})
}
