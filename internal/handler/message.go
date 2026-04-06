package handler

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"mailapi/internal/model"
	"mailapi/internal/store"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (h *Handler) ListMessages(c *gin.Context) {
	accountID := c.GetString("accountId")
	oid, err := bson.ObjectIDFromHex(accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid account"})
		return
	}

	// 向后兼容：默认仍支持 page/itemsPerPage 的 skip 分页。
	// 性能优化：当提供 cursor/after 时，使用 seek（cursor）分页避免深度 skip 的性能灾难。
	cursor := strings.TrimSpace(c.Query("cursor"))
	if cursor == "" {
		cursor = strings.TrimSpace(c.Query("after"))
	}

	perPage, _ := strconv.Atoi(c.DefaultQuery("itemsPerPage", "30"))
	if perPage < 1 || perPage > 100 {
		perPage = 30
	}

	var (
		messages []model.Message
		total    int64
	)
	if cursor != "" {
		messages, total, err = h.store.ListMessagesAfter(c.Request.Context(), oid, cursor, perPage)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrInvalidID):
				c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid cursor"})
			case errors.Is(err, store.ErrNotFound):
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "cursor not found"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to list messages"})
			}
			return
		}
	} else {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		if page < 1 {
			page = 1
		}

		messages, total, err = h.store.ListMessages(c.Request.Context(), oid, page, perPage)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to list messages"})
			return
		}
	}

	// Set download URLs
	for i := range messages {
		messages[i].DownloadURL = "/messages/" + messages[i].ID.Hex() + "/download"
	}

	// Seek 分页建议使用 nextCursor（包含 createdAt+id），可避免后续分页额外的“cursor -> createdAt”查询。
	var nextCursor string
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		nextCursor = fmt.Sprintf("%d.%s", last.CreatedAt.UnixMilli(), last.ID.Hex())
	}

	c.JSON(http.StatusOK, model.HydraCollection{
		Context:    "/contexts/Message",
		ID:         "/messages",
		Type:       "hydra:Collection",
		TotalItems: total,
		Member:     messages,
		NextCursor: nextCursor,
	})
}

func (h *Handler) GetMessage(c *gin.Context) {
	accountID := c.GetString("accountId")
	id := c.Param("id")

	msg, err := h.store.GetMessage(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidID) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "message not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	if msg.AccountID.Hex() != accountID {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "access denied"})
		return
	}

	msg.DownloadURL = "/messages/" + msg.ID.Hex() + "/download"
	c.JSON(http.StatusOK, msg)
}

func (h *Handler) UpdateMessage(c *gin.Context) {
	accountID := c.GetString("accountId")
	id := c.Param("id")

	// Verify ownership（只取元数据，避免高并发下无谓解码 text/html/rawMessage）
	msg, err := h.store.GetMessageMeta(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidID) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "message not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	if msg.AccountID.Hex() != accountID {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "access denied"})
		return
	}

	var req model.UpdateMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "invalid request"})
		return
	}

	// keep 参数受环境变量控制：默认不允许（避免被滥用导致无限存储增长）。
	if req.Keep != nil && !h.allowMessageKeep {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "message keep is disabled"})
		return
	}

	var seenToSet *bool
	var keepToSet *bool
	if req.Seen != nil && msg.Seen != *req.Seen {
		seenToSet = req.Seen
	}
	if req.Keep != nil && msg.Keep != *req.Keep {
		keepToSet = req.Keep
	}

	// 性能优化：seen/keep 合并成一次 UpdateOne，降低 Mongo 往返次数。
	if seenToSet != nil || keepToSet != nil {
		if err := h.store.UpdateMessageFlags(c.Request.Context(), id, seenToSet, keepToSet); err != nil {
			if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidID) {
				c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "message not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to update message"})
			return
		}
		if seenToSet != nil {
			msg.Seen = *seenToSet
		}
		if keepToSet != nil {
			msg.Keep = *keepToSet
		}
	}

	msg.DownloadURL = "/messages/" + msg.ID.Hex() + "/download"
	c.JSON(http.StatusOK, msg)
}

func (h *Handler) DeleteMessage(c *gin.Context) {
	accountID := c.GetString("accountId")
	id := c.Param("id")

	// 删除只需要元数据（所有权/是否有附件），避免加载 raw/text/html 大字段。
	msg, err := h.store.GetMessageMeta(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidID) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "message not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	if msg.AccountID.Hex() != accountID {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "access denied"})
		return
	}

	if err := h.store.DeleteMessage(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to delete message"})
		return
	}

	// Cleanup objects in background (rawMessage/attachments share the same prefix).
	h.tryAsyncStorageCleanup(id)

	c.Status(http.StatusNoContent)
}

func (h *Handler) DownloadMessage(c *gin.Context) {
	accountID := c.GetString("accountId")
	id := c.Param("id")

	// 下载只需要 rawMessage + 所有者信息，避免把正文/附件元数据等一起拉出来。
	msg, err := h.store.GetMessageRaw(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidID) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "message not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	if msg.AccountID.Hex() != accountID {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "access denied"})
		return
	}

	c.Header("Content-Disposition", contentDispositionAttachment(id+".eml"))
	if len(msg.RawMessage) > 0 {
		c.Data(http.StatusOK, "message/rfc822", msg.RawMessage)
		return
	}

	// 新版本优先将 rawMessage 存放在对象存储中，Mongo 仅保存元数据；这里做兼容回退。
	obj, contentType, size, err := h.storage.OpenRawMessage(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "source not available"})
		return
	}
	defer obj.Close()
	c.DataFromReader(http.StatusOK, size, contentType, obj, nil)
}

func (h *Handler) DownloadAttachment(c *gin.Context) {
	accountID := c.GetString("accountId")
	msgID := c.Param("id")
	attachmentID := c.Param("attachmentId")

	// 附件下载只需要附件元数据，避免加载 raw/text/html 大字段。
	msg, err := h.store.GetMessageMeta(c.Request.Context(), msgID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidID) {
			c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "message not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "internal error"})
		return
	}

	if msg.AccountID.Hex() != accountID {
		c.JSON(http.StatusForbidden, gin.H{"code": 403, "message": "access denied"})
		return
	}

	// Find the attachment metadata
	var att *model.Attachment
	for _, a := range msg.Attachments {
		if a.ID == attachmentID {
			att = &a
			break
		}
	}
	if att == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 404, "message": "attachment not found"})
		return
	}

	obj, contentType, size, err := h.storage.Open(c.Request.Context(), msgID, attachmentID, att.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "failed to download attachment"})
		return
	}
	defer obj.Close()

	c.Header("Content-Disposition", contentDispositionAttachment(att.Filename))
	c.DataFromReader(http.StatusOK, size, contentType, obj, nil)
}

func contentDispositionAttachment(filename string) string {
	filename = sanitizeFilenameForHeader(filename, "attachment")
	return mime.FormatMediaType("attachment", map[string]string{"filename": filename})
}

// sanitizeFilenameForHeader 清理用户可控的文件名，避免响应头注入与异常值。
// 注意：这是“header 层”的保护，不影响对象存储 key。
func sanitizeFilenameForHeader(filename, fallback string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return fallback
	}

	// 去掉 CR/LF/NUL 等危险字符，避免 header 注入。
	filename = strings.ReplaceAll(filename, "\r", "")
	filename = strings.ReplaceAll(filename, "\n", "")
	filename = strings.ReplaceAll(filename, "\x00", "")

	// 去掉控制字符，避免日志/代理异常；同时把路径分隔符替换为 '_'。
	filename = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\':
			return '_'
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, filename)
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return fallback
	}

	// 过长文件名会导致部分代理/浏览器行为异常，这里做上限保护（按 rune 计数）。
	const maxRunes = 200
	if utf8.RuneCountInString(filename) > maxRunes {
		rs := []rune(filename)
		filename = string(rs[:maxRunes])
		filename = strings.TrimSpace(filename)
		if filename == "" {
			return fallback
		}
	}

	return filename
}
