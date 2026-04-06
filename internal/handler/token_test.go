package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"mailapi/internal/model"
	"mailapi/internal/store"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/crypto/bcrypt"
)

func TestCreateToken_Success(t *testing.T) {
	oid := bson.NewObjectID()
	hashed, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	ms := &mockStore{
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			return &model.Account{
				ID:       oid,
				Address:  "user@example.com",
				Password: string(hashed),
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"user@example.com","password":"secret123"}`
	c, w := newTestContext("POST", "/token", []byte(body))
	h.CreateToken(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp model.TokenResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Token == "" {
		t.Error("expected non-empty token")
	}
	if resp.ID != oid.Hex() {
		t.Errorf("id = %q, want %q", resp.ID, oid.Hex())
	}
}

func TestCreateToken_NormalizesAddress(t *testing.T) {
	var gotAddr string
	oid := bson.NewObjectID()
	hashed, _ := bcrypt.GenerateFromPassword([]byte("secret123"), bcrypt.MinCost)
	ms := &mockStore{
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			gotAddr = address
			return &model.Account{
				ID:       oid,
				Address:  "user@example.com",
				Password: string(hashed),
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"USER@Example.com","password":"secret123"}`
	c, w := newTestContext("POST", "/token", []byte(body))
	h.CreateToken(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if gotAddr != "user@example.com" {
		t.Fatalf("normalized address=%q want %q", gotAddr, "user@example.com")
	}
}

func TestCreateToken_InvalidBody(t *testing.T) {
	h := newTestHandler(&mockStore{}, &mockCache{}, &mockStorage{})

	c, w := newTestContext("POST", "/token", []byte(`{}`))
	h.CreateToken(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateToken_MissingPassword(t *testing.T) {
	h := newTestHandler(&mockStore{}, &mockCache{}, &mockStorage{})

	body := `{"address":"user@example.com"}`
	c, w := newTestContext("POST", "/token", []byte(body))
	h.CreateToken(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateToken_AccountNotFound(t *testing.T) {
	ms := &mockStore{
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			return nil, store.ErrNotFound
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"user@example.com","password":"secret123"}`
	c, w := newTestContext("POST", "/token", []byte(body))
	h.CreateToken(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCreateToken_WrongPassword(t *testing.T) {
	hashed, _ := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	ms := &mockStore{
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			return &model.Account{
				ID:       bson.NewObjectID(),
				Address:  "user@example.com",
				Password: string(hashed),
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"user@example.com","password":"wrong-password"}`
	c, w := newTestContext("POST", "/token", []byte(body))
	h.CreateToken(c)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCreateToken_ResponseFormat(t *testing.T) {
	oid := bson.NewObjectID()
	hashed, _ := bcrypt.GenerateFromPassword([]byte("pass123456"), bcrypt.MinCost)
	ms := &mockStore{
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			return &model.Account{ID: oid, Address: address, Password: string(hashed)}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"address":"user@example.com","password":"pass123456"}`
	c, w := newTestContext("POST", "/token", []byte(body))
	h.CreateToken(c)

	var raw map[string]any
	json.Unmarshal(w.Body.Bytes(), &raw)

	if _, ok := raw["token"]; !ok {
		t.Error("response missing 'token' field")
	}
	if _, ok := raw["id"]; !ok {
		t.Error("response missing 'id' field")
	}
}
