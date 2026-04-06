package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"mailapi/internal/auth"
	"mailapi/internal/model"
	"mailapi/internal/prefix"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// --- Mock Store ---

type mockStore struct {
	pingFunc                        func(ctx context.Context) error
	listDomainsFunc                 func(ctx context.Context) ([]model.Domain, error)
	getDomainByNameFunc             func(ctx context.Context, domain string) (*model.Domain, error)
	createDomainFunc                func(ctx context.Context, domain *model.Domain) error
	createAccountFunc               func(ctx context.Context, account *model.Account) error
	getAccountFunc                  func(ctx context.Context, id string) (*model.Account, error)
	getAccountIDByAddressFunc       func(ctx context.Context, address string) (bson.ObjectID, error)
	getAccountByAddressFunc         func(ctx context.Context, address string) (*model.Account, error)
	deleteAccountFunc               func(ctx context.Context, id string) error
	updateAccountUsedFunc           func(ctx context.Context, id bson.ObjectID, delta int64) error
	createMessageFunc               func(ctx context.Context, msg *model.Message) error
	getMessageFunc                  func(ctx context.Context, id string) (*model.Message, error)
	getMessageMetaFunc              func(ctx context.Context, id string) (*model.Message, error)
	getMessageRawFunc               func(ctx context.Context, id string) (*model.Message, error)
	hasMessageFunc                  func(ctx context.Context, id string) (bool, error)
	hasMessageByIngestFunc          func(ctx context.Context, accountID bson.ObjectID, ingestStream string, ingestSeq int64) (bool, error)
	existingMessageIDsFunc          func(ctx context.Context, ids []string) (map[string]struct{}, error)
	listMessagesFunc                func(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error)
	listMessagesAfterFunc           func(ctx context.Context, accountID bson.ObjectID, cursorID string, limit int) ([]model.Message, int64, error)
	updateMessageFlagsFunc          func(ctx context.Context, id string, seen, keep *bool) error
	updateMessageSeenFunc           func(ctx context.Context, id string, seen bool) error
	updateMessageKeepFunc           func(ctx context.Context, id string, keep bool) error
	deleteMessageFunc               func(ctx context.Context, id string) error
	hardDeleteMessagesByAccountFunc func(ctx context.Context, accountID bson.ObjectID) ([]model.Message, error)
	countMessagesByAccountFunc      func(ctx context.Context, accountID bson.ObjectID) (int64, error)
	closeFunc                       func(ctx context.Context) error
}

func (m *mockStore) Ping(ctx context.Context) error {
	if m.pingFunc != nil {
		return m.pingFunc(ctx)
	}
	return nil
}

func (m *mockStore) ListDomains(ctx context.Context) ([]model.Domain, error) {
	if m.listDomainsFunc != nil {
		return m.listDomainsFunc(ctx)
	}
	return []model.Domain{}, nil
}

func (m *mockStore) GetDomainByName(ctx context.Context, domain string) (*model.Domain, error) {
	if m.getDomainByNameFunc != nil {
		return m.getDomainByNameFunc(ctx, domain)
	}
	return nil, nil
}

func (m *mockStore) CreateDomain(ctx context.Context, domain *model.Domain) error {
	if m.createDomainFunc != nil {
		return m.createDomainFunc(ctx, domain)
	}
	return nil
}

func (m *mockStore) SyncDomains(ctx context.Context, domains []model.Domain) error {
	return nil
}

func (m *mockStore) CreateAccount(ctx context.Context, account *model.Account) error {
	if m.createAccountFunc != nil {
		return m.createAccountFunc(ctx, account)
	}
	return nil
}

func (m *mockStore) GetAccount(ctx context.Context, id string) (*model.Account, error) {
	if m.getAccountFunc != nil {
		return m.getAccountFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockStore) GetAccountIDByAddress(ctx context.Context, address string) (bson.ObjectID, error) {
	if m.getAccountIDByAddressFunc != nil {
		return m.getAccountIDByAddressFunc(ctx, address)
	}
	return bson.ObjectID{}, nil
}

func (m *mockStore) GetAccountByAddress(ctx context.Context, address string) (*model.Account, error) {
	if m.getAccountByAddressFunc != nil {
		return m.getAccountByAddressFunc(ctx, address)
	}
	return nil, nil
}

func (m *mockStore) DeleteAccount(ctx context.Context, id string) error {
	if m.deleteAccountFunc != nil {
		return m.deleteAccountFunc(ctx, id)
	}
	return nil
}

func (m *mockStore) UpdateAccountUsed(ctx context.Context, id bson.ObjectID, delta int64) error {
	if m.updateAccountUsedFunc != nil {
		return m.updateAccountUsedFunc(ctx, id, delta)
	}
	return nil
}

func (m *mockStore) CreateMessage(ctx context.Context, msg *model.Message) error {
	if m.createMessageFunc != nil {
		return m.createMessageFunc(ctx, msg)
	}
	return nil
}

func (m *mockStore) GetMessage(ctx context.Context, id string) (*model.Message, error) {
	if m.getMessageFunc != nil {
		return m.getMessageFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockStore) GetMessageMeta(ctx context.Context, id string) (*model.Message, error) {
	if m.getMessageMetaFunc != nil {
		return m.getMessageMetaFunc(ctx, id)
	}
	// 兼容：部分旧测试只设置了 getMessageFunc。
	if m.getMessageFunc != nil {
		return m.getMessageFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockStore) GetMessageRaw(ctx context.Context, id string) (*model.Message, error) {
	if m.getMessageRawFunc != nil {
		return m.getMessageRawFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockStore) HasMessage(ctx context.Context, id string) (bool, error) {
	if m.hasMessageFunc != nil {
		return m.hasMessageFunc(ctx, id)
	}
	return false, nil
}

func (m *mockStore) HasMessageByIngest(ctx context.Context, accountID bson.ObjectID, ingestStream string, ingestSeq int64) (bool, error) {
	if m.hasMessageByIngestFunc != nil {
		return m.hasMessageByIngestFunc(ctx, accountID, ingestStream, ingestSeq)
	}
	return false, nil
}

func (m *mockStore) ExistingMessageIDs(ctx context.Context, ids []string) (map[string]struct{}, error) {
	if m.existingMessageIDsFunc != nil {
		return m.existingMessageIDsFunc(ctx, ids)
	}
	return map[string]struct{}{}, nil
}

func (m *mockStore) ListMessages(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error) {
	if m.listMessagesFunc != nil {
		return m.listMessagesFunc(ctx, accountID, page, perPage)
	}
	return []model.Message{}, 0, nil
}

func (m *mockStore) ListMessagesAfter(ctx context.Context, accountID bson.ObjectID, cursorID string, limit int) ([]model.Message, int64, error) {
	if m.listMessagesAfterFunc != nil {
		return m.listMessagesAfterFunc(ctx, accountID, cursorID, limit)
	}
	return []model.Message{}, 0, nil
}

func (m *mockStore) UpdateMessageFlags(ctx context.Context, id string, seen, keep *bool) error {
	if m.updateMessageFlagsFunc != nil {
		return m.updateMessageFlagsFunc(ctx, id, seen, keep)
	}
	if seen != nil && m.updateMessageSeenFunc != nil {
		if err := m.updateMessageSeenFunc(ctx, id, *seen); err != nil {
			return err
		}
	}
	if keep != nil && m.updateMessageKeepFunc != nil {
		if err := m.updateMessageKeepFunc(ctx, id, *keep); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockStore) UpdateMessageSeen(ctx context.Context, id string, seen bool) error {
	if m.updateMessageSeenFunc != nil {
		return m.updateMessageSeenFunc(ctx, id, seen)
	}
	return nil
}

func (m *mockStore) UpdateMessageKeep(ctx context.Context, id string, keep bool) error {
	if m.updateMessageKeepFunc != nil {
		return m.updateMessageKeepFunc(ctx, id, keep)
	}
	return nil
}

func (m *mockStore) DeleteMessage(ctx context.Context, id string) error {
	if m.deleteMessageFunc != nil {
		return m.deleteMessageFunc(ctx, id)
	}
	return nil
}

func (m *mockStore) HardDeleteMessagesByAccount(ctx context.Context, accountID bson.ObjectID) ([]model.Message, error) {
	if m.hardDeleteMessagesByAccountFunc != nil {
		return m.hardDeleteMessagesByAccountFunc(ctx, accountID)
	}
	return nil, nil
}

func (m *mockStore) CountMessagesByAccount(ctx context.Context, accountID bson.ObjectID) (int64, error) {
	if m.countMessagesByAccountFunc != nil {
		return m.countMessagesByAccountFunc(ctx, accountID)
	}
	return 0, nil
}

func (m *mockStore) Close(ctx context.Context) error {
	if m.closeFunc != nil {
		return m.closeFunc(ctx)
	}
	return nil
}

// --- Mock Cache ---

type mockCache struct {
	pingFunc                    func(ctx context.Context) error
	setAddressFunc              func(ctx context.Context, addr string, ttl time.Duration) error
	hasAddressFunc              func(ctx context.Context, addr string) (bool, error)
	removeAddressFunc           func(ctx context.Context, addr string) error
	publishFunc                 func(ctx context.Context, accountID string, payload string) error
	subscribeFunc               func(ctx context.Context, accountID string) *redis.PubSub
	checkRateLimitFunc          func(ctx context.Context, key string, limit int64, window time.Duration) (bool, error)
	checkSMTPRateLimitFunc      func(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error)
	checkKeyRateLimitFunc       func(ctx context.Context, apiKey string, limit int64, window time.Duration) (bool, error)
	checkKeyDomainRateLimitFunc func(ctx context.Context, apiKey, domain string, limit int64, window time.Duration) (bool, error)
	clientFunc                  func() *redis.Client
	closeFunc                   func() error
}

func (m *mockCache) Ping(ctx context.Context) error {
	if m.pingFunc != nil {
		return m.pingFunc(ctx)
	}
	return nil
}

func (m *mockCache) SetAddress(ctx context.Context, addr string, ttl time.Duration) error {
	if m.setAddressFunc != nil {
		return m.setAddressFunc(ctx, addr, ttl)
	}
	return nil
}

func (m *mockCache) HasAddress(ctx context.Context, addr string) (bool, error) {
	if m.hasAddressFunc != nil {
		return m.hasAddressFunc(ctx, addr)
	}
	return false, nil
}

func (m *mockCache) RemoveAddress(ctx context.Context, addr string) error {
	if m.removeAddressFunc != nil {
		return m.removeAddressFunc(ctx, addr)
	}
	return nil
}

func (m *mockCache) Publish(ctx context.Context, accountID string, payload string) error {
	if m.publishFunc != nil {
		return m.publishFunc(ctx, accountID, payload)
	}
	return nil
}

func (m *mockCache) Subscribe(ctx context.Context, accountID string) *redis.PubSub {
	if m.subscribeFunc != nil {
		return m.subscribeFunc(ctx, accountID)
	}
	return nil
}

func (m *mockCache) CheckRateLimit(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	if m.checkRateLimitFunc != nil {
		return m.checkRateLimitFunc(ctx, key, limit, window)
	}
	return true, nil
}

func (m *mockCache) CheckSMTPRateLimit(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error) {
	if m.checkSMTPRateLimitFunc != nil {
		return m.checkSMTPRateLimitFunc(ctx, ip, limit, window)
	}
	return true, nil
}

func (m *mockCache) CheckKeyRateLimit(ctx context.Context, apiKey string, limit int64, window time.Duration) (bool, error) {
	if m.checkKeyRateLimitFunc != nil {
		return m.checkKeyRateLimitFunc(ctx, apiKey, limit, window)
	}
	return true, nil
}

func (m *mockCache) CheckKeyDomainRateLimit(ctx context.Context, apiKey, domain string, limit int64, window time.Duration) (bool, error) {
	if m.checkKeyDomainRateLimitFunc != nil {
		return m.checkKeyDomainRateLimitFunc(ctx, apiKey, domain, limit, window)
	}
	return true, nil
}

func (m *mockCache) Client() *redis.Client {
	if m.clientFunc != nil {
		return m.clientFunc()
	}
	return nil
}

func (m *mockCache) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// --- Mock Storage ---

type mockStorage struct {
	healthFunc           func(ctx context.Context) error
	uploadFunc           func(ctx context.Context, messageID, attachmentID, filename, contentType string, data []byte) error
	uploadReaderFunc     func(ctx context.Context, messageID, attachmentID, filename, contentType string, r io.Reader, size int64) error
	openFunc             func(ctx context.Context, messageID, attachmentID, filename string) (io.ReadCloser, string, int64, error)
	downloadFunc         func(ctx context.Context, messageID, attachmentID, filename string) ([]byte, string, error)
	getPresignedURLFunc  func(ctx context.Context, messageID, attachmentID, filename string, expiry time.Duration) (string, error)
	deleteByMessageFunc  func(ctx context.Context, messageID string) error
	listMessageIDsFunc   func(ctx context.Context, startAfter string, maxObjects int) ([]string, string, error)
	uploadRawMessageFunc func(ctx context.Context, messageID string, data []byte) error
	openRawMessageFunc   func(ctx context.Context, messageID string) (io.ReadCloser, string, int64, error)
}

func (m *mockStorage) Health(ctx context.Context) error {
	if m.healthFunc != nil {
		return m.healthFunc(ctx)
	}
	return nil
}

func (m *mockStorage) Upload(ctx context.Context, messageID, attachmentID, filename, contentType string, data []byte) error {
	if m.uploadFunc != nil {
		return m.uploadFunc(ctx, messageID, attachmentID, filename, contentType, data)
	}
	return nil
}

func (m *mockStorage) UploadReader(ctx context.Context, messageID, attachmentID, filename, contentType string, r io.Reader, size int64) error {
	if m.uploadReaderFunc != nil {
		return m.uploadReaderFunc(ctx, messageID, attachmentID, filename, contentType, r, size)
	}
	return nil
}

func (m *mockStorage) Open(ctx context.Context, messageID, attachmentID, filename string) (io.ReadCloser, string, int64, error) {
	if m.openFunc != nil {
		return m.openFunc(ctx, messageID, attachmentID, filename)
	}
	return nil, "", 0, errors.New("mockStorage.Open not implemented")
}

func (m *mockStorage) Download(ctx context.Context, messageID, attachmentID, filename string) ([]byte, string, error) {
	if m.downloadFunc != nil {
		return m.downloadFunc(ctx, messageID, attachmentID, filename)
	}
	return nil, "", nil
}

func (m *mockStorage) GetPresignedURL(ctx context.Context, messageID, attachmentID, filename string, expiry time.Duration) (string, error) {
	if m.getPresignedURLFunc != nil {
		return m.getPresignedURLFunc(ctx, messageID, attachmentID, filename, expiry)
	}
	return "", nil
}

func (m *mockStorage) DeleteByMessage(ctx context.Context, messageID string) error {
	if m.deleteByMessageFunc != nil {
		return m.deleteByMessageFunc(ctx, messageID)
	}
	return nil
}

func (m *mockStorage) ListMessageIDs(ctx context.Context, startAfter string, maxObjects int) ([]string, string, error) {
	if m.listMessageIDsFunc != nil {
		return m.listMessageIDsFunc(ctx, startAfter, maxObjects)
	}
	return []string{}, "", nil
}

func (m *mockStorage) UploadRawMessage(ctx context.Context, messageID string, data []byte) error {
	if m.uploadRawMessageFunc != nil {
		return m.uploadRawMessageFunc(ctx, messageID, data)
	}
	return nil
}

func (m *mockStorage) OpenRawMessage(ctx context.Context, messageID string) (io.ReadCloser, string, int64, error) {
	if m.openRawMessageFunc != nil {
		return m.openRawMessageFunc(ctx, messageID)
	}
	return nil, "", 0, errors.New("mockStorage.OpenRawMessage not implemented")
}

// --- Test helpers ---

func newTestHandler(s *mockStore, c *mockCache, st *mockStorage) *Handler {
	return newTestHandlerWithKeep(s, c, st, false)
}

func newTestHandlerWithKeep(s *mockStore, c *mockCache, st *mockStorage, allowMessageKeep bool) *Handler {
	a := auth.New("test-secret", time.Hour)
	pg := prefix.New()
	return New(s, c, st, a, 168*time.Hour, nil, pg, 0, 0, allowMessageKeep)
}

func newTestContext(method, path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	if body != nil {
		c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		c.Request = httptest.NewRequest(method, path, nil)
	}
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func setAuth(c *gin.Context, accountID, address string) {
	c.Set("accountId", accountID)
	c.Set("address", address)
}

// serveRequest routes a request through a full gin Engine so that status codes
// are properly flushed to the recorder (needed for c.Status(204) etc.).
func serveRequest(h *Handler, method, routePath, requestPath string, body []byte, accountID, address string) *httptest.ResponseRecorder {
	r := gin.New()
	handler := func(c *gin.Context) {
		if accountID != "" {
			c.Set("accountId", accountID)
			c.Set("address", address)
		}
		switch method + " " + routePath {
		case "DELETE /accounts/:id":
			h.DeleteAccount(c)
		case "DELETE /messages/:id":
			h.DeleteMessage(c)
		}
	}
	r.Handle(method, routePath, handler)

	w := httptest.NewRecorder()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, requestPath, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, requestPath, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}
