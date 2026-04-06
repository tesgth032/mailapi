package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mailapi/internal/store"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type bulkUpdateMessagesRequest struct {
	IDs  []string `json:"ids"`
	All  bool     `json:"all"`
	Seen *bool    `json:"seen"`
	Keep *bool    `json:"keep"`
}

type bulkDeleteMessagesRequest struct {
	IDs []string `json:"ids"`
}

// BulkUpdateMessages 批量更新消息 flags（seen/keep）。
// - 默认更新指定 ids；当 all=true 时更新该账号下所有消息。
// - keep 参数受 allowMessageKeep 开关控制。
func (h *Handler) BulkUpdateMessages(c *gin.Context) {
	accountID := c.GetString("accountId")
	oid, err := bson.ObjectIDFromHex(accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid account"})
		return
	}

	var req bulkUpdateMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
		return
	}
	if req.Seen == nil && req.Keep == nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "at least one of seen/keep is required"})
		return
	}
	if req.Keep != nil && !h.allowMessageKeep {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "message keep is disabled"})
		return
	}

	if !req.All && len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "ids is required unless all=true"})
		return
	}
	if req.All && len(req.IDs) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "ids must be empty when all=true"})
		return
	}
	if len(req.IDs) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "too many ids (max 2000)"})
		return
	}

	var modified int64
	if req.All {
		modified, err = h.store.BulkUpdateMessageFlagsByAccount(c.Request.Context(), oid, req.Seen, req.Keep)
	} else {
		modified, err = h.store.BulkUpdateMessageFlagsByIDs(c.Request.Context(), oid, req.IDs, req.Seen, req.Keep)
	}
	if err != nil {
		if errors.Is(err, store.ErrInvalidID) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid id"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to update messages"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"modified": modified})
}

// DeleteMessages 批量软删除消息（按账号维度）。支持 seen=true/false 条件删除。
func (h *Handler) DeleteMessages(c *gin.Context) {
	accountID := c.GetString("accountId")
	oid, err := bson.ObjectIDFromHex(accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid account"})
		return
	}

	// 条件：seen=true/false（可选）
	var seenFilter *bool
	if s := strings.TrimSpace(c.Query("seen")); s != "" {
		v, err := strconv.ParseBool(s)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid seen filter"})
			return
		}
		seenFilter = &v
	}

	limit := 5000
	if s := strings.TrimSpace(c.Query("limit")); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid limit"})
			return
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 20000 {
		limit = 20000
	}

	ids, totalSize, err := h.store.SoftDeleteMessagesByAccount(c.Request.Context(), oid, seenFilter, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to delete messages"})
		return
	}

	if totalSize != 0 {
		_ = h.store.UpdateAccountUsed(c.Request.Context(), oid, -totalSize)
	}
	// 异步纠偏：批量删除对 used 的影响很大，尽量用聚合再校准一次，降低漂移风险。
	if len(ids) > 0 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, _ = h.store.RecalculateAccountUsed(ctx, oid)
			cancel()
		}()
	}

	for _, id := range ids {
		h.tryAsyncStorageCleanup(id)
	}

	c.JSON(http.StatusOK, gin.H{
		"deleted":    len(ids),
		"totalSize":  totalSize,
		"truncated":  len(ids) == limit,
		"seenFilter": seenFilter,
	})
}

// BulkDeleteMessagesByIDs 按指定 ids 批量软删除消息。
func (h *Handler) BulkDeleteMessagesByIDs(c *gin.Context) {
	accountID := c.GetString("accountId")
	oid, err := bson.ObjectIDFromHex(accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid account"})
		return
	}

	var req bulkDeleteMessagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
		return
	}
	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "ids is required"})
		return
	}
	if len(req.IDs) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "too many ids (max 2000)"})
		return
	}

	ids, totalSize, err := h.store.SoftDeleteMessagesByIDs(c.Request.Context(), oid, req.IDs)
	if err != nil {
		if err == store.ErrInvalidID {
			c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid id"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to delete messages"})
		return
	}

	if totalSize != 0 {
		_ = h.store.UpdateAccountUsed(c.Request.Context(), oid, -totalSize)
	}
	if len(ids) > 0 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			_, _ = h.store.RecalculateAccountUsed(ctx, oid)
			cancel()
		}()
	}

	for _, id := range ids {
		h.tryAsyncStorageCleanup(id)
	}

	c.JSON(http.StatusOK, gin.H{"deleted": len(ids), "totalSize": totalSize})
}
