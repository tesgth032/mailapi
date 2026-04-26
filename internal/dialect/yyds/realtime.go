package yyds

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mailapi/internal/cache"
	"mailapi/internal/model"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/net/websocket"
)

type accountEventStream interface {
	Next(ctx context.Context) (string, error)
	Close() error
}

type redisAccountEventStream struct {
	sub *redis.PubSub
	ch  <-chan *redis.Message
}

type publishedMessageEvent struct {
	Type    string        `json:"@type"`
	ID      string        `json:"id"`
	Subject string        `json:"subject"`
	From    model.Address `json:"from"`
	Intro   string        `json:"intro,omitempty"`
	Seen    bool          `json:"seen"`
}

type realtimeEnvelope struct {
	Type    string      `json:"type"`
	Mailbox string      `json:"mailbox,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

var openAccountEventStream = func(ca cache.Interface, accountID string) (accountEventStream, error) {
	if ca == nil {
		return nil, fmt.Errorf("cache not configured")
	}

	sub := ca.Subscribe(context.Background(), accountID)
	return &redisAccountEventStream{
		sub: sub,
		ch:  sub.Channel(redis.WithChannelSize(256)),
	}, nil
}

func (s *redisAccountEventStream) Next(ctx context.Context) (string, error) {
	if s == nil {
		return "", io.EOF
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case msg, ok := <-s.ch:
		if !ok || msg == nil {
			return "", io.EOF
		}
		return msg.Payload, nil
	}
}

func (s *redisAccountEventStream) Close() error {
	if s == nil || s.sub == nil {
		return nil
	}
	return s.sub.Close()
}

func (a *API) createWSTicket(c *gin.Context) {
	account, errResp := a.messageListAccount(c)
	if errResp != nil {
		abortError(c, errResp.status, errResp.code, errResp.message)
		return
	}
	if a.auth == nil {
		abortError(c, http.StatusServiceUnavailable, "auth_not_configured", "Auth not configured")
		return
	}

	expiry := 5 * time.Minute
	if a.tokenTTL > 0 && a.tokenTTL < expiry {
		expiry = a.tokenTTL
	}

	token, err := a.auth.GenerateTokenWithExpiry(account.ID.Hex(), account.Address, expiry)
	if err != nil {
		abortError(c, http.StatusInternalServerError, "internal_error", "Failed to create websocket ticket")
		return
	}

	writeSuccess(c, http.StatusOK, gin.H{
		"token":     token,
		"ticket":    token,
		"address":   strings.ToLower(account.Address),
		"expiresAt": time.Now().Add(expiry),
	})
}

func (a *API) handleWS(c *gin.Context) {
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		abortError(c, http.StatusUnauthorized, "authorization_required_ws_ticket", "WebSocket ticket required")
		return
	}
	if a.auth == nil {
		abortError(c, http.StatusServiceUnavailable, "auth_not_configured", "Auth not configured")
		return
	}

	claims, err := a.auth.ValidateToken(token)
	if err != nil {
		abortError(c, http.StatusUnauthorized, "invalid_or_expired_ws_ticket", "Invalid or expired WebSocket ticket")
		return
	}

	stream, err := openAccountEventStream(a.cache, claims.AccountID)
	if err != nil {
		abortError(c, http.StatusServiceUnavailable, "realtime_not_available", "Realtime stream not available")
		return
	}
	defer stream.Close()

	handler := websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()

		ctx := c.Request.Context()
		mailbox := strings.ToLower(strings.TrimSpace(claims.Address))
		for {
			payload, err := stream.Next(ctx)
			if err != nil {
				return
			}

			message, err := a.translateRealtimePayload(ctx, mailbox, payload)
			if err != nil {
				return
			}
			if len(message) == 0 {
				continue
			}
			if err := websocket.Message.Send(ws, string(message)); err != nil {
				return
			}
		}
	})

	handler.ServeHTTP(c.Writer, c.Request)
}

func (a *API) translateRealtimePayload(ctx context.Context, mailbox string, payload string) ([]byte, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, nil
	}

	var passthrough realtimeEnvelope
	if err := json.Unmarshal([]byte(payload), &passthrough); err == nil && passthrough.Type != "" {
		if passthrough.Mailbox == "" {
			passthrough.Mailbox = mailbox
		}
		return json.Marshal(passthrough)
	}

	var published publishedMessageEvent
	if err := json.Unmarshal([]byte(payload), &published); err == nil &&
		(published.ID != "" || published.Subject != "" || published.Type != "" || published.From.Address != "" || published.From.Name != "") {
		eventTime := time.Now().UTC()
		if published.ID != "" && a.store != nil {
			if msg, err := a.store.GetMessageMeta(ctx, published.ID); err == nil && !msg.CreatedAt.IsZero() {
				eventTime = msg.CreatedAt
			}
		}

		return json.Marshal(realtimeEnvelope{
			Type:    "message.new",
			Mailbox: mailbox,
			Data: gin.H{
				"id":      published.ID,
				"from":    published.From,
				"subject": published.Subject,
				"date":    eventTime,
				"intro":   published.Intro,
				"seen":    published.Seen,
			},
		})
	}

	return json.Marshal(realtimeEnvelope{
		Type:    "message.raw",
		Mailbox: mailbox,
		Data: gin.H{
			"raw": payload,
		},
	})
}
