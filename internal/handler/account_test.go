package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"mailapi/internal/middleware"
	"mailapi/internal/model"
	"mailapi/internal/store"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCreateAccount_Success(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			account.ID = bson.NewObjectID()
			return nil
		},
	}
	mc := &mockCache{}
	h := newTestHandler(ms, mc, &mockStorage{})

	body := `{"address":"user@example.com","password":"secret123"}`
	c, w := newTestContext("POST", "/accounts", []byte(body))
	h.CreateAccount(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp model.Account
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Address != "user@example.com" {
		t.Errorf("address = %q, want %q", resp.Address, "user@example.com")
	}
}

func TestCreateAccount_InvalidBody(t *testing.T) {
	h := newTestHandler(&mockStore{}, &mockCache{}, &mockStorage{})
	c, w := newTestContext("POST", "/accounts", []byte(`{}`))
	h.CreateAccount(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateAccount_PasswordTooShort(t *testing.T) {
	h := newTestHandler(&mockStore{}, &mockCache{}, &mockStorage{})
	body := `{"address":"user@example.com","password":"abc"}`
	c, w := newTestContext("POST", "/accounts", []byte(body))
	h.CreateAccount(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateAccount_InvalidEmail(t *testing.T) {
	h := newTestHandler(&mockStore{}, &mockCache{}, &mockStorage{})
	body := `{"address":"nodomainemail","password":"secret123"}`
	c, w := newTestContext("POST", "/accounts", []byte(body))
	h.CreateAccount(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateAccount_DomainNotFound(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"user@unknown.com","password":"secret123"}`
	c, w := newTestContext("POST", "/accounts", []byte(body))
	h.CreateAccount(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateAccount_PrivateDomainRequiresExplicitAuthorization(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "private.com", IsActive: true, IsPrivate: true}}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"user@private.com","password":"secret123"}`
	c, w := newTestContext("POST", "/accounts", []byte(body))
	h.CreateAccount(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestCreateAccount_PrivateDomainExplicitAuthorization_Success(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "private.com", IsActive: true, IsPrivate: true}}, nil
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			account.ID = bson.NewObjectID()
			return nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"user@private.com","password":"secret123"}`
	c, w := newTestContext("POST", "/accounts", []byte(body))
	// 模拟 wildcard API key，但显式授权 private.com。
	c.Set(middleware.CtxAPIKeyInfo, &middleware.APIKeyInfo{
		Name:      "Test",
		Domains:   []string{"*"},
		DomainSet: map[string]struct{}{"private.com": {}},
		Wildcard:  true,
	})
	c.Set(middleware.CtxAPIKeyDomains, []string{"*"})

	h.CreateAccount(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusCreated, w.Body.String())
	}
}

func TestCreateAccount_DuplicateAddress(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			return store.ErrDuplicateKey
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"user@example.com","password":"secret123"}`
	c, w := newTestContext("POST", "/accounts", []byte(body))
	h.CreateAccount(c)

	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestCreateAccount_AddressLowercased(t *testing.T) {
	var savedAddress string
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			savedAddress = account.Address
			account.ID = bson.NewObjectID()
			return nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"User@Example.COM","password":"secret123"}`
	c, w := newTestContext("POST", "/accounts", []byte(body))
	h.CreateAccount(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusCreated)
	}
	if savedAddress != "user@example.com" {
		t.Errorf("saved address = %q, want %q", savedAddress, "user@example.com")
	}
}

func TestCreateAccount_SetsRedisCache(t *testing.T) {
	var cachedAddr string
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			account.ID = bson.NewObjectID()
			return nil
		},
	}
	mc := &mockCache{
		setAddressFunc: func(ctx context.Context, addr string, ttl time.Duration) error {
			cachedAddr = addr
			return nil
		},
	}
	h := newTestHandler(ms, mc, &mockStorage{})

	body := `{"address":"user@example.com","password":"secret123"}`
	c, w := newTestContext("POST", "/accounts", []byte(body))
	h.CreateAccount(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
	if cachedAddr != "user@example.com" {
		t.Errorf("cached address = %q, want %q", cachedAddr, "user@example.com")
	}
}

func TestGetAccount_Success(t *testing.T) {
	oid := bson.NewObjectID()
	ms := &mockStore{
		getAccountFunc: func(ctx context.Context, id string) (*model.Account, error) {
			return &model.Account{ID: oid, Address: "user@example.com"}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/accounts/"+oid.Hex(), nil)
	c.Params = gin.Params{{Key: "id", Value: oid.Hex()}}
	setAuth(c, oid.Hex(), "user@example.com")
	h.GetAccount(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestGetAccount_NotOwner(t *testing.T) {
	oid := bson.NewObjectID()
	h := newTestHandler(&mockStore{}, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/accounts/"+oid.Hex(), nil)
	c.Params = gin.Params{{Key: "id", Value: oid.Hex()}}
	setAuth(c, bson.NewObjectID().Hex(), "other@example.com") // different ID
	h.GetAccount(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestGetAccount_NotFound(t *testing.T) {
	oid := bson.NewObjectID()
	ms := &mockStore{
		getAccountFunc: func(ctx context.Context, id string) (*model.Account, error) {
			return nil, store.ErrNotFound
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/accounts/"+oid.Hex(), nil)
	c.Params = gin.Params{{Key: "id", Value: oid.Hex()}}
	setAuth(c, oid.Hex(), "user@example.com")
	h.GetAccount(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetMe_Success(t *testing.T) {
	oid := bson.NewObjectID()
	ms := &mockStore{
		getAccountFunc: func(ctx context.Context, id string) (*model.Account, error) {
			return &model.Account{ID: oid, Address: "me@example.com"}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/me", nil)
	setAuth(c, oid.Hex(), "me@example.com")
	h.GetMe(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp model.Account
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Address != "me@example.com" {
		t.Errorf("address = %q", resp.Address)
	}
}

func TestGetMe_NotFound(t *testing.T) {
	ms := &mockStore{
		getAccountFunc: func(ctx context.Context, id string) (*model.Account, error) {
			return nil, store.ErrNotFound
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/me", nil)
	setAuth(c, bson.NewObjectID().Hex(), "user@example.com")
	h.GetMe(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDeleteAccount_Success(t *testing.T) {
	oid := bson.NewObjectID()
	var removedAddr string
	ms := &mockStore{
		getAccountFunc: func(ctx context.Context, id string) (*model.Account, error) {
			return &model.Account{ID: oid, Address: "user@example.com"}, nil
		},
		hardDeleteMessagesByAccountFunc: func(ctx context.Context, accountID bson.ObjectID) ([]model.Message, error) {
			return nil, nil
		},
		deleteAccountFunc: func(ctx context.Context, id string) error {
			return nil
		},
	}
	mc := &mockCache{
		removeAddressFunc: func(ctx context.Context, addr string) error {
			removedAddr = addr
			return nil
		},
	}
	h := newTestHandler(ms, mc, &mockStorage{})

	w := serveRequest(h, "DELETE", "/accounts/:id", "/accounts/"+oid.Hex(), nil, oid.Hex(), "user@example.com")

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if removedAddr != "user@example.com" {
		t.Errorf("removed address = %q, want %q", removedAddr, "user@example.com")
	}
}

func TestDeleteAccount_NotOwner(t *testing.T) {
	oid := bson.NewObjectID()
	h := newTestHandler(&mockStore{}, &mockCache{}, &mockStorage{})

	c, w := newTestContext("DELETE", "/accounts/"+oid.Hex(), nil)
	c.Params = gin.Params{{Key: "id", Value: oid.Hex()}}
	setAuth(c, bson.NewObjectID().Hex(), "other@example.com")
	h.DeleteAccount(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestDeleteAccount_NotFound(t *testing.T) {
	oid := bson.NewObjectID()
	ms := &mockStore{
		getAccountFunc: func(ctx context.Context, id string) (*model.Account, error) {
			return nil, store.ErrNotFound
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("DELETE", "/accounts/"+oid.Hex(), nil)
	c.Params = gin.Params{{Key: "id", Value: oid.Hex()}}
	setAuth(c, oid.Hex(), "user@example.com")
	h.DeleteAccount(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDeleteAccount_CleansUpAttachments(t *testing.T) {
	oid := bson.NewObjectID()
	msgID := bson.NewObjectID()
	var deletedMsgID string
	done := make(chan struct{})
	ms := &mockStore{
		getAccountFunc: func(ctx context.Context, id string) (*model.Account, error) {
			return &model.Account{ID: oid, Address: "user@example.com"}, nil
		},
		hardDeleteMessagesByAccountFunc: func(ctx context.Context, accountID bson.ObjectID) ([]model.Message, error) {
			return []model.Message{
				{ID: msgID, HasAttachments: true},
			}, nil
		},
		deleteAccountFunc: func(ctx context.Context, id string) error {
			return nil
		},
	}
	mst := &mockStorage{
		deleteByMessageFunc: func(ctx context.Context, messageID string) error {
			deletedMsgID = messageID
			close(done)
			return nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, mst)

	w := serveRequest(h, "DELETE", "/accounts/:id", "/accounts/"+oid.Hex(), nil, oid.Hex(), "user@example.com")

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for storage cleanup")
	}
	if deletedMsgID != msgID.Hex() {
		t.Errorf("deleted message ID = %q, want %q", deletedMsgID, msgID.Hex())
	}
}

func TestCreateAccount_PasswordNotInResponse(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{{Domain: "example.com", IsActive: true}}, nil
		},
		createAccountFunc: func(ctx context.Context, account *model.Account) error {
			account.ID = bson.NewObjectID()
			return nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"user@example.com","password":"secret123"}`
	c, w := newTestContext("POST", "/accounts", []byte(body))
	h.CreateAccount(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}

	var raw map[string]any
	json.Unmarshal(w.Body.Bytes(), &raw)
	if _, ok := raw["password"]; ok {
		t.Error("password field should not appear in response")
	}
}
