package yyds

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"mailapi/internal/auth"
	"mailapi/internal/cache"
	"mailapi/internal/config"
	"mailapi/internal/middleware"
	"mailapi/internal/model"
	"mailapi/internal/prefix"
	"mailapi/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/net/websocket"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type testStore struct {
	listDomainsFunc          func(ctx context.Context) ([]model.Domain, error)
	getDomainByNameFunc      func(ctx context.Context, domain string) (*model.Domain, error)
	createAccountFunc        func(ctx context.Context, account *model.Account) error
	getAccountFunc           func(ctx context.Context, id string) (*model.Account, error)
	getAccountByAddressFunc  func(ctx context.Context, address string) (*model.Account, error)
	countMessagesFunc        func(ctx context.Context, accountID bson.ObjectID) (int64, error)
	listMessagesFunc         func(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error)
	listMessagesFilteredFunc func(ctx context.Context, accountID bson.ObjectID, page, perPage int, seen *bool) ([]model.Message, int64, error)
	getMessageFunc           func(ctx context.Context, id string) (*model.Message, error)
	getMessageMetaFunc       func(ctx context.Context, id string) (*model.Message, error)
	getMessageRawFunc        func(ctx context.Context, id string) (*model.Message, error)
	updateMessageFlagsFunc   func(ctx context.Context, id string, seen, keep *bool) error
	bulkUpdateByAccountFunc  func(ctx context.Context, accountID bson.ObjectID, seen, keep *bool) (int64, error)
}

func (m *testStore) Ping(ctx context.Context) error { return nil }
func (m *testStore) ListDomains(ctx context.Context) ([]model.Domain, error) {
	if m.listDomainsFunc != nil {
		return m.listDomainsFunc(ctx)
	}
	return []model.Domain{}, nil
}
func (m *testStore) GetDomainByName(ctx context.Context, domain string) (*model.Domain, error) {
	if m.getDomainByNameFunc != nil {
		return m.getDomainByNameFunc(ctx, domain)
	}
	return nil, store.ErrNotFound
}
func (m *testStore) CreateDomain(ctx context.Context, domain *model.Domain) error  { return nil }
func (m *testStore) SyncDomains(ctx context.Context, domains []model.Domain) error { return nil }
func (m *testStore) CreateAccount(ctx context.Context, account *model.Account) error {
	if m.createAccountFunc != nil {
		return m.createAccountFunc(ctx, account)
	}
	return nil
}
func (m *testStore) GetAccount(ctx context.Context, id string) (*model.Account, error) {
	if m.getAccountFunc != nil {
		return m.getAccountFunc(ctx, id)
	}
	return nil, store.ErrNotFound
}
func (m *testStore) GetAccountIDByAddress(ctx context.Context, address string) (bson.ObjectID, error) {
	return bson.ObjectID{}, store.ErrNotFound
}
func (m *testStore) GetAccountByAddress(ctx context.Context, address string) (*model.Account, error) {
	if m.getAccountByAddressFunc != nil {
		return m.getAccountByAddressFunc(ctx, address)
	}
	return nil, store.ErrNotFound
}
func (m *testStore) DeleteAccount(ctx context.Context, id string) error { return nil }
func (m *testStore) UpdateAccountUsed(ctx context.Context, id bson.ObjectID, delta int64) error {
	return nil
}
func (m *testStore) TryReserveAccountUsed(ctx context.Context, id bson.ObjectID, delta int64) (bool, error) {
	return true, nil
}
func (m *testStore) RecalculateAccountUsed(ctx context.Context, id bson.ObjectID) (int64, error) {
	return 0, nil
}
func (m *testStore) CreateMessage(ctx context.Context, msg *model.Message) error { return nil }
func (m *testStore) GetMessage(ctx context.Context, id string) (*model.Message, error) {
	if m.getMessageFunc != nil {
		return m.getMessageFunc(ctx, id)
	}
	return nil, store.ErrNotFound
}
func (m *testStore) GetMessageMeta(ctx context.Context, id string) (*model.Message, error) {
	if m.getMessageMetaFunc != nil {
		return m.getMessageMetaFunc(ctx, id)
	}
	if m.getMessageFunc != nil {
		return m.getMessageFunc(ctx, id)
	}
	return nil, store.ErrNotFound
}
func (m *testStore) GetMessageRaw(ctx context.Context, id string) (*model.Message, error) {
	if m.getMessageRawFunc != nil {
		return m.getMessageRawFunc(ctx, id)
	}
	return nil, store.ErrNotFound
}
func (m *testStore) HasMessage(ctx context.Context, id string) (bool, error) { return false, nil }
func (m *testStore) HasMessageByIngest(ctx context.Context, accountID bson.ObjectID, ingestStream string, ingestSeq int64) (bool, error) {
	return false, nil
}
func (m *testStore) ExistingMessageIDs(ctx context.Context, ids []string) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}
func (m *testStore) ListMessages(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error) {
	if m.listMessagesFunc != nil {
		return m.listMessagesFunc(ctx, accountID, page, perPage)
	}
	return []model.Message{}, 0, nil
}
func (m *testStore) ListMessagesFiltered(ctx context.Context, accountID bson.ObjectID, page, perPage int, seen *bool) ([]model.Message, int64, error) {
	if m.listMessagesFilteredFunc != nil {
		return m.listMessagesFilteredFunc(ctx, accountID, page, perPage, seen)
	}
	return []model.Message{}, 0, nil
}
func (m *testStore) ListMessagesAfter(ctx context.Context, accountID bson.ObjectID, cursorID string, limit int) ([]model.Message, int64, error) {
	return []model.Message{}, 0, nil
}
func (m *testStore) ListMessagesAfterFiltered(ctx context.Context, accountID bson.ObjectID, cursorID string, limit int, seen *bool) ([]model.Message, int64, error) {
	return []model.Message{}, 0, nil
}
func (m *testStore) UpdateMessageFlags(ctx context.Context, id string, seen, keep *bool) error {
	if m.updateMessageFlagsFunc != nil {
		return m.updateMessageFlagsFunc(ctx, id, seen, keep)
	}
	return nil
}
func (m *testStore) BulkUpdateMessageFlagsByIDs(ctx context.Context, accountID bson.ObjectID, ids []string, seen, keep *bool) (int64, error) {
	return 0, nil
}
func (m *testStore) BulkUpdateMessageFlagsByAccount(ctx context.Context, accountID bson.ObjectID, seen, keep *bool) (int64, error) {
	if m.bulkUpdateByAccountFunc != nil {
		return m.bulkUpdateByAccountFunc(ctx, accountID, seen, keep)
	}
	return 0, nil
}
func (m *testStore) UpdateMessageSeen(ctx context.Context, id string, seen bool) error { return nil }
func (m *testStore) UpdateMessageKeep(ctx context.Context, id string, keep bool) error { return nil }
func (m *testStore) DeleteMessage(ctx context.Context, id string) error                { return nil }
func (m *testStore) SoftDeleteMessagesByAccount(ctx context.Context, accountID bson.ObjectID, seen *bool, limit int) ([]string, int64, error) {
	return nil, 0, nil
}
func (m *testStore) SoftDeleteMessagesByIDs(ctx context.Context, accountID bson.ObjectID, ids []string) ([]string, int64, error) {
	return nil, 0, nil
}
func (m *testStore) HardDeleteMessagesByAccount(ctx context.Context, accountID bson.ObjectID) ([]model.Message, error) {
	return nil, nil
}
func (m *testStore) CountMessagesByAccount(ctx context.Context, accountID bson.ObjectID) (int64, error) {
	if m.countMessagesFunc != nil {
		return m.countMessagesFunc(ctx, accountID)
	}
	return 0, nil
}
func (m *testStore) Close(ctx context.Context) error { return nil }

type testCache struct {
	setAddressFunc func(ctx context.Context, addr string, ttl time.Duration) error
}

func (m *testCache) Ping(ctx context.Context) error { return nil }
func (m *testCache) SetAddress(ctx context.Context, addr string, ttl time.Duration) error {
	if m.setAddressFunc != nil {
		return m.setAddressFunc(ctx, addr, ttl)
	}
	return nil
}
func (m *testCache) HasAddress(ctx context.Context, addr string) (bool, error) { return false, nil }
func (m *testCache) RemoveAddress(ctx context.Context, addr string) error      { return nil }
func (m *testCache) Publish(ctx context.Context, accountID string, payload string) error {
	return nil
}
func (m *testCache) Subscribe(ctx context.Context, accountID string) *redis.PubSub { return nil }
func (m *testCache) CheckRateLimit(ctx context.Context, key string, limit int64, window time.Duration) (bool, error) {
	return true, nil
}
func (m *testCache) CheckSMTPRateLimit(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error) {
	return true, nil
}
func (m *testCache) CheckKeyRateLimit(ctx context.Context, apiKey string, limit int64, window time.Duration) (bool, error) {
	return true, nil
}
func (m *testCache) CheckKeyDomainRateLimit(ctx context.Context, apiKey, domain string, limit int64, window time.Duration) (bool, error) {
	return true, nil
}
func (m *testCache) Client() *redis.Client { return nil }
func (m *testCache) Close() error          { return nil }

type testStorage struct{}

func (s *testStorage) Health(ctx context.Context) error { return nil }
func (s *testStorage) Upload(ctx context.Context, messageID, attachmentID, filename, contentType string, data []byte) error {
	return nil
}
func (s *testStorage) UploadReader(ctx context.Context, messageID, attachmentID, filename, contentType string, r io.Reader, size int64) error {
	return nil
}
func (s *testStorage) Open(ctx context.Context, messageID, attachmentID, filename string) (io.ReadCloser, string, int64, error) {
	return nil, "", 0, nil
}
func (s *testStorage) Download(ctx context.Context, messageID, attachmentID, filename string) ([]byte, string, error) {
	return nil, "", nil
}
func (s *testStorage) GetPresignedURL(ctx context.Context, messageID, attachmentID, filename string, expiry time.Duration) (string, error) {
	return "", nil
}
func (s *testStorage) DeleteByMessage(ctx context.Context, messageID string) error { return nil }
func (s *testStorage) ListMessageIDs(ctx context.Context, startAfter string, maxObjects int) ([]string, string, error) {
	return nil, "", nil
}
func (s *testStorage) UploadRawMessage(ctx context.Context, messageID string, data []byte) error {
	return nil
}
func (s *testStorage) OpenRawMessage(ctx context.Context, messageID string) (io.ReadCloser, string, int64, error) {
	return nil, "", 0, nil
}

type testEventStream struct {
	ch chan string
}

func (s *testEventStream) Next(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case payload, ok := <-s.ch:
		if !ok {
			return "", io.EOF
		}
		return payload, nil
	}
}

func (s *testEventStream) Close() error {
	if s == nil || s.ch == nil {
		return nil
	}
	close(s.ch)
	s.ch = nil
	return nil
}

func newTestServer(t *testing.T, st store.Interface, ca cache.Interface) http.Handler {
	t.Helper()
	return New(Config{
		Store:      st,
		Cache:      ca,
		Storage:    &testStorage{},
		Auth:       auth.New("test-secret", time.Hour),
		AccountTTL: 24 * time.Hour,
		APIKeys:    nil,
		Prefix:     prefix.New(),
		Public:     config.YYDSDialectConfig{},
	})
}

func performJSON(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCreateAccount_OpenModeReturnsToken(t *testing.T) {
	now := time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)
	accountID := bson.NewObjectID()
	st := &testStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			if domain == "example.com" {
				return &model.Domain{Domain: "example.com", IsActive: true}, nil
			}
			return nil, store.ErrNotFound
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			account.ID = accountID
			account.CreatedAt = now
			return nil
		},
	}
	h := newTestServer(t, st, &testCache{})

	w := performJSON(h, http.MethodPost, "/v1/accounts", `{"localPart":"demo","domain":"example.com"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool            `json:"success"`
		Data    accountResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success body=%s", w.Body.String())
	}
	if resp.Data.Address != "demo@example.com" {
		t.Fatalf("address=%q", resp.Data.Address)
	}
	if resp.Data.Token == "" {
		t.Fatal("expected token")
	}
	if resp.Data.InboxType != "temp" || resp.Data.Source != "api" {
		t.Fatalf("unexpected data=%+v", resp.Data)
	}
}

func TestCreateToken_PublicDomainNoAuth(t *testing.T) {
	accountID := bson.NewObjectID()
	st := &testStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			if domain == "example.com" {
				return &model.Domain{Domain: "example.com", IsActive: true}, nil
			}
			return nil, store.ErrNotFound
		},
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			return &model.Account{ID: accountID, Address: "demo@example.com"}, nil
		},
	}
	h := newTestServer(t, st, &testCache{})

	w := performJSON(h, http.MethodPost, "/v1/token", `{"address":"demo@example.com"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool          `json:"success"`
		Data    tokenResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success || resp.Data.Token == "" {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
	if resp.Data.Address != "demo@example.com" {
		t.Fatalf("address=%q", resp.Data.Address)
	}
}

func TestCreateWildcardAccount_UsesConfiguredParentDomain(t *testing.T) {
	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	accountID := bson.NewObjectID()
	var createdAddress string
	st := &testStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			if domain == "example.com" {
				return &model.Domain{Domain: "example.com", IsActive: true}, nil
			}
			return nil, store.ErrNotFound
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			createdAddress = account.Address
			account.ID = accountID
			account.CreatedAt = now
			return nil
		},
	}
	h := newTestServer(t, st, &testCache{})

	w := performJSON(h, http.MethodPost, "/v1/accounts/wildcard", `{"localPart":"demo","domain":"example.com","subdomain":"mail"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if createdAddress != "demo@mail.example.com" {
		t.Fatalf("createdAddress=%q", createdAddress)
	}
}

func TestCreateWildcardAccount_GeneratesRandomChildDomain(t *testing.T) {
	now := time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC)
	accountID := bson.NewObjectID()
	var createdAddress string
	st := &testStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			if domain == "example.com" {
				return &model.Domain{Domain: "example.com", IsActive: true}, nil
			}
			return nil, store.ErrNotFound
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			createdAddress = account.Address
			account.ID = accountID
			account.CreatedAt = now
			return nil
		},
	}
	h := newTestServer(t, st, &testCache{})

	w := performJSON(h, http.MethodPost, "/v1/accounts/wildcard", `{"localPart":"demo","domain":"example.com"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.HasPrefix(createdAddress, "demo@w") || !strings.HasSuffix(createdAddress, ".example.com") {
		t.Fatalf("createdAddress=%q", createdAddress)
	}
	re := regexp.MustCompile(`^demo@w[0-9a-f]{12}\.example\.com$`)
	if !re.MatchString(createdAddress) {
		t.Fatalf("createdAddress=%q does not match generated child-domain pattern", createdAddress)
	}
}

func TestCreateWildcardAccount_PrivateParentRequiresExplicitAPIKeyAuthorization(t *testing.T) {
	now := time.Date(2026, 4, 27, 11, 0, 0, 0, time.UTC)
	accountID := bson.NewObjectID()
	var createdAddress string
	st := &testStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true, IsPrivate: true}}, nil
		},
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			if domain == "example.com" {
				return &model.Domain{Domain: "example.com", IsActive: true, IsPrivate: true}, nil
			}
			return nil, store.ErrNotFound
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			createdAddress = account.Address
			account.ID = accountID
			account.CreatedAt = now
			return nil
		},
	}
	h := New(Config{
		Store:      st,
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       auth.New("test-secret", time.Hour),
		AccountTTL: 24 * time.Hour,
		APIKeys: map[string]*middleware.APIKeyInfo{
			"test-key": {
				Name:    "Test",
				Domains: []string{"example.com"},
			},
		},
		Prefix: prefix.New(),
		Public: config.YYDSDialectConfig{},
	})

	w := performJSON(h, http.MethodPost, "/v1/accounts/wildcard", `{"localPart":"demo","domain":"example.com","subdomain":"mail"}`, map[string]string{
		"X-API-Key": "test-key",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if createdAddress != "demo@mail.example.com" {
		t.Fatalf("createdAddress=%q", createdAddress)
	}
}

func TestCreateAccount_UsesAPIKeyDefaultDomain(t *testing.T) {
	now := time.Date(2026, 4, 27, 11, 30, 0, 0, time.UTC)
	accountID := bson.NewObjectID()
	var createdAddress string
	st := &testStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{
				{Domain: "example.com", IsActive: true},
				{Domain: "public.test", IsActive: true},
			}, nil
		},
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			switch domain {
			case "example.com", "public.test":
				return &model.Domain{Domain: domain, IsActive: true}, nil
			default:
				return nil, store.ErrNotFound
			}
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			createdAddress = account.Address
			account.ID = accountID
			account.CreatedAt = now
			return nil
		},
	}
	h := New(Config{
		Store:      st,
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       auth.New("test-secret", time.Hour),
		AccountTTL: 24 * time.Hour,
		APIKeys: map[string]*middleware.APIKeyInfo{
			"test-key": {
				Name:          "Test",
				Domains:       []string{"example.com", "public.test"},
				DomainSet:     map[string]struct{}{"example.com": {}, "public.test": {}},
				DefaultDomain: "public.test",
			},
		},
		Prefix: prefix.New(),
	})

	w := performJSON(h, http.MethodPost, "/v1/accounts", `{"localPart":"demo"}`, map[string]string{
		"X-API-Key": "test-key",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if createdAddress != "demo@public.test" {
		t.Fatalf("createdAddress=%q", createdAddress)
	}
}

func TestCreateWildcardAccount_UsesLegacyWildcardFields(t *testing.T) {
	now := time.Date(2026, 4, 27, 11, 45, 0, 0, time.UTC)
	accountID := bson.NewObjectID()
	var createdAddress string
	st := &testStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			if domain == "example.com" {
				return &model.Domain{Domain: "example.com", IsActive: true}, nil
			}
			return nil, store.ErrNotFound
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			createdAddress = account.Address
			account.ID = accountID
			account.CreatedAt = now
			return nil
		},
	}
	h := newTestServer(t, st, &testCache{})

	w := performJSON(h, http.MethodPost, "/v1/accounts/wildcard", `{"localPart":"demo","wildcardRuleId":"example.com","subdomainLabel":"mail"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if createdAddress != "demo@mail.example.com" {
		t.Fatalf("createdAddress=%q", createdAddress)
	}
}

func TestCreateWildcardAccount_UsesAPIKeyDefaultSubdomain(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	accountID := bson.NewObjectID()
	var createdAddress string
	st := &testStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			if domain == "example.com" {
				return &model.Domain{Domain: "example.com", IsActive: true}, nil
			}
			return nil, store.ErrNotFound
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			createdAddress = account.Address
			account.ID = accountID
			account.CreatedAt = now
			return nil
		},
	}
	h := New(Config{
		Store:      st,
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       auth.New("test-secret", time.Hour),
		AccountTTL: 24 * time.Hour,
		APIKeys: map[string]*middleware.APIKeyInfo{
			"test-key": {
				Name:             "Test",
				Domains:          []string{"example.com"},
				DomainSet:        map[string]struct{}{"example.com": {}},
				DefaultDomain:    "example.com",
				DefaultSubdomain: "team",
			},
		},
		Prefix: prefix.New(),
	})

	w := performJSON(h, http.MethodPost, "/v1/accounts/wildcard", `{"localPart":"demo"}`, map[string]string{
		"X-API-Key": "test-key",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if createdAddress != "demo@team.example.com" {
		t.Fatalf("createdAddress=%q", createdAddress)
	}
}

func TestCreateWildcardAccount_RandomParentDomainWhenOmitted(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 15, 0, 0, time.UTC)
	accountID := bson.NewObjectID()
	var createdAddress string
	st := &testStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{
				{Domain: "example.com", IsActive: true},
				{Domain: "example.org", IsActive: true},
			}, nil
		},
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			switch domain {
			case "example.com", "example.org":
				return &model.Domain{Domain: domain, IsActive: true}, nil
			default:
				return nil, store.ErrNotFound
			}
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			createdAddress = account.Address
			account.ID = accountID
			account.CreatedAt = now
			return nil
		},
	}
	h := newTestServer(t, st, &testCache{})

	w := performJSON(h, http.MethodPost, "/v1/accounts/wildcard", `{"localPart":"demo"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	re := regexp.MustCompile(`^demo@w[0-9a-f]{12}\.(example\.com|example\.org)$`)
	if !re.MatchString(createdAddress) {
		t.Fatalf("createdAddress=%q", createdAddress)
	}
}

func TestMarkMessagesRead_UsesQueryAddress(t *testing.T) {
	accountID := bson.NewObjectID()
	st := &testStore{
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			if domain == "example.com" {
				return &model.Domain{Domain: "example.com", IsActive: true}, nil
			}
			return nil, store.ErrNotFound
		},
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			if address != "demo@example.com" {
				t.Fatalf("address=%q", address)
			}
			return &model.Account{ID: accountID, Address: address}, nil
		},
		bulkUpdateByAccountFunc: func(ctx context.Context, oid bson.ObjectID, seen, keep *bool) (int64, error) {
			if oid != accountID {
				t.Fatalf("accountID=%s", oid.Hex())
			}
			if seen == nil || !*seen {
				t.Fatalf("seen=%v", seen)
			}
			return 3, nil
		},
		countMessagesFunc: func(ctx context.Context, oid bson.ObjectID) (int64, error) {
			return 5, nil
		},
	}
	h := New(Config{
		Store:      st,
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       auth.New("test-secret", time.Hour),
		AccountTTL: 24 * time.Hour,
		APIKeys: map[string]*middleware.APIKeyInfo{
			"test-key": {
				Name:      "Test",
				Domains:   []string{"example.com"},
				DomainSet: map[string]struct{}{"example.com": {}},
			},
		},
		Prefix: prefix.New(),
	})

	w := performJSON(h, http.MethodPost, "/v1/messages/mark-read?address=demo@example.com", "", map[string]string{
		"X-API-Key": "test-key",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Updated     int64 `json:"updated"`
			AlreadySeen int64 `json:"alreadySeen"`
			Total       int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success || resp.Data.Updated != 3 || resp.Data.AlreadySeen != 2 || resp.Data.Total != 5 {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
}

func TestListMessages_WithTempToken(t *testing.T) {
	accountID := bson.NewObjectID()
	messageID := bson.NewObjectID()
	au := auth.New("test-secret", time.Hour)
	token, err := au.GenerateToken(accountID.Hex(), "demo@example.com")
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	st := &testStore{
		getAccountFunc: func(ctx context.Context, id string) (*model.Account, error) {
			return &model.Account{ID: accountID, Address: "demo@example.com"}, nil
		},
		listMessagesFunc: func(ctx context.Context, oid bson.ObjectID, page, perPage int) ([]model.Message, int64, error) {
			return []model.Message{{
				ID:        messageID,
				AccountID: accountID,
				From:      model.Address{Address: "sender@example.org"},
				To:        []model.Address{{Address: "demo@example.com"}},
				Subject:   "hello",
				Seen:      false,
				Size:      123,
				CreatedAt: time.Now(),
			}}, 1, nil
		},
		listMessagesFilteredFunc: func(ctx context.Context, oid bson.ObjectID, page, perPage int, seen *bool) ([]model.Message, int64, error) {
			return []model.Message{}, 1, nil
		},
	}
	h := New(Config{
		Store:      st,
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       au,
		AccountTTL: 24 * time.Hour,
		Prefix:     prefix.New(),
	})

	w := performJSON(h, http.MethodGet, "/v1/messages", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool                `json:"success"`
		Data    messageListEnvelope `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success || resp.Data.Total != 1 || resp.Data.UnreadCount != 1 {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
	if len(resp.Data.Messages) != 1 || resp.Data.Messages[0].InboxIDSnake != accountID.Hex() {
		t.Fatalf("unexpected messages=%+v", resp.Data.Messages)
	}
}

func TestGetMessageSource_WrapsRawSource(t *testing.T) {
	accountID := bson.NewObjectID()
	messageID := bson.NewObjectID()
	au := auth.New("test-secret", time.Hour)
	token, err := au.GenerateToken(accountID.Hex(), "demo@example.com")
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	st := &testStore{
		getMessageRawFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:         messageID,
				AccountID:  accountID,
				RawMessage: []byte("From: demo@example.org\r\n\r\nhello"),
			}, nil
		},
	}
	h := New(Config{
		Store:      st,
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       au,
		AccountTTL: 24 * time.Hour,
		Prefix:     prefix.New(),
	})

	w := performJSON(h, http.MethodGet, "/v1/sources/"+messageID.Hex(), "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			ID  string `json:"id"`
			Raw string `json:"raw"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success || resp.Data.ID != messageID.Hex() || !strings.Contains(resp.Data.Raw, "hello") {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
}

func TestGetMessageSource_AliasRoute(t *testing.T) {
	accountID := bson.NewObjectID()
	messageID := bson.NewObjectID()
	au := auth.New("test-secret", time.Hour)
	token, err := au.GenerateToken(accountID.Hex(), "demo@example.com")
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	st := &testStore{
		getMessageRawFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:         messageID,
				AccountID:  accountID,
				RawMessage: []byte("Subject: alias\r\n\r\nhello"),
			}, nil
		},
	}
	h := New(Config{
		Store:      st,
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       au,
		AccountTTL: 24 * time.Hour,
		Prefix:     prefix.New(),
	})

	w := performJSON(h, http.MethodGet, "/v1/messages/"+messageID.Hex()+"/source", "", map[string]string{
		"Authorization": "Bearer " + token,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "alias") {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
}

func TestListWildcardRules_ReturnsVisibleDomains(t *testing.T) {
	st := &testStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{
				{Domain: "example.com", IsActive: true},
				{Domain: "private.test", IsActive: true, IsPrivate: true},
			}, nil
		},
	}
	h := New(Config{
		Store:      st,
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       auth.New("test-secret", time.Hour),
		AccountTTL: 24 * time.Hour,
		APIKeys: map[string]*middleware.APIKeyInfo{
			"test-key": {
				Name:      "Test",
				Domains:   []string{"example.com", "private.test"},
				DomainSet: map[string]struct{}{"example.com": {}, "private.test": {}},
			},
		},
		Prefix: prefix.New(),
	})

	w := performJSON(h, http.MethodGet, "/v1/me/wildcard-rules", "", map[string]string{
		"X-API-Key": "test-key",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool    `json:"success"`
		Data    []gin.H `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success || len(resp.Data) != 2 {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
	if resp.Data[0]["id"] == "" || resp.Data[0]["domain"] == "" {
		t.Fatalf("unexpected data=%+v", resp.Data)
	}
}

func TestGetQuota_ReturnsEffectivePlanSnapshot(t *testing.T) {
	h := New(Config{
		Store:      &testStore{},
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       auth.New("test-secret", time.Hour),
		AccountTTL: 24 * time.Hour,
		APIKeys: map[string]*middleware.APIKeyInfo{
			"test-key": {
				Name:     "Test",
				Domains:  []string{"*"},
				Wildcard: true,
			},
		},
		Prefix: prefix.New(),
		Public: config.YYDSDialectConfig{
			Plans: []config.YYDSPlanConfig{
				{
					ID:               "pro",
					Name:             "Pro",
					MaxDomains:       5,
					MaxInboxes:       10,
					MaxWildcardRules: 3,
					MaxRPS:           20,
					IsActive:         true,
				},
			},
		},
	})

	w := performJSON(h, http.MethodGet, "/v1/me/quota", "", map[string]string{
		"X-API-Key": "test-key",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Plan struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"plan"`
			BonusDaily    int64 `json:"bonusDaily"`
			PurchasedPool int64 `json:"purchasedPool"`
			WildcardRules int64 `json:"wildcardRules"`
			Dimensions    struct {
				MaxDomains int64 `json:"maxDomains"`
				MaxRps     int64 `json:"maxRps"`
			} `json:"dimensions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success || resp.Data.Plan.ID != "pro" || resp.Data.Dimensions.MaxDomains != 5 || resp.Data.WildcardRules != 3 || resp.Data.Dimensions.MaxRps != 20 {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
}

func TestCreateWSTicket_ByAddress(t *testing.T) {
	accountID := bson.NewObjectID()
	au := auth.New("test-secret", time.Hour)
	st := &testStore{
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			if domain == "example.com" {
				return &model.Domain{Domain: "example.com", IsActive: true}, nil
			}
			return nil, store.ErrNotFound
		},
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			return &model.Account{ID: accountID, Address: address}, nil
		},
	}
	h := New(Config{
		Store:      st,
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       au,
		AccountTTL: 24 * time.Hour,
		APIKeys: map[string]*middleware.APIKeyInfo{
			"test-key": {
				Name:      "Test",
				Domains:   []string{"example.com"},
				DomainSet: map[string]struct{}{"example.com": {}},
			},
		},
		Prefix: prefix.New(),
	})

	w := performJSON(h, http.MethodGet, "/v1/auth/ws-ticket?address=demo@example.com", "", map[string]string{
		"X-API-Key": "test-key",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Token   string `json:"token"`
			Ticket  string `json:"ticket"`
			Address string `json:"address"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Success || resp.Data.Token == "" || resp.Data.Token != resp.Data.Ticket || resp.Data.Address != "demo@example.com" {
		t.Fatalf("unexpected body=%s", w.Body.String())
	}
	claims, err := au.ValidateToken(resp.Data.Token)
	if err != nil {
		t.Fatalf("validate token: %v", err)
	}
	if claims.AccountID != accountID.Hex() || claims.Address != "demo@example.com" {
		t.Fatalf("claims=%+v", claims)
	}
}

func TestHandleWS_PushesRealtimeMessage(t *testing.T) {
	accountID := bson.NewObjectID()
	messageID := bson.NewObjectID()
	eventStream := &testEventStream{ch: make(chan string, 1)}
	oldOpen := openAccountEventStream
	openAccountEventStream = func(ca cache.Interface, account string) (accountEventStream, error) {
		if account != accountID.Hex() {
			t.Fatalf("account=%q", account)
		}
		return eventStream, nil
	}
	t.Cleanup(func() {
		openAccountEventStream = oldOpen
		_ = eventStream.Close()
	})

	au := auth.New("test-secret", time.Hour)
	st := &testStore{
		getDomainByNameFunc: func(ctx context.Context, domain string) (*model.Domain, error) {
			if domain == "example.com" {
				return &model.Domain{Domain: "example.com", IsActive: true}, nil
			}
			return nil, store.ErrNotFound
		},
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			return &model.Account{ID: accountID, Address: address}, nil
		},
		getMessageMetaFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{ID: messageID, AccountID: accountID, CreatedAt: time.Date(2026, 4, 27, 13, 0, 0, 0, time.UTC)}, nil
		},
	}
	h := New(Config{
		Store:      st,
		Cache:      &testCache{},
		Storage:    &testStorage{},
		Auth:       au,
		AccountTTL: 24 * time.Hour,
		APIKeys: map[string]*middleware.APIKeyInfo{
			"test-key": {
				Name:      "Test",
				Domains:   []string{"example.com"},
				DomainSet: map[string]struct{}{"example.com": {}},
			},
		},
		Prefix: prefix.New(),
	})

	server := httptest.NewServer(h)
	defer server.Close()

	w := performJSON(h, http.MethodGet, "/v1/auth/ws-ticket?address=demo@example.com", "", map[string]string{
		"X-API-Key": "test-key",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("ticket status=%d body=%s", w.Code, w.Body.String())
	}
	var ticketResp struct {
		Success bool `json:"success"`
		Data    struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ticketResp); err != nil {
		t.Fatalf("unmarshal ticket: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/ws?token=" + url.QueryEscape(ticketResp.Data.Token)
	ws, err := websocket.Dial(wsURL, "", "http://localhost/")
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer ws.Close()

	eventStream.ch <- `{"@type":"Message","id":"` + messageID.Hex() + `","subject":"hello","from":{"address":"sender@example.org"}}`

	var payload string
	if err := websocket.Message.Receive(ws, &payload); err != nil {
		t.Fatalf("receive websocket message: %v", err)
	}
	if !strings.Contains(payload, `"type":"message.new"`) || !strings.Contains(payload, `"mailbox":"demo@example.com"`) || !strings.Contains(payload, `"subject":"hello"`) {
		t.Fatalf("payload=%s", payload)
	}
}
