package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"mailapi/internal/middleware"
	"mailapi/internal/model"
)

func TestListDomains_Success(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{
				{Domain: "example.com", IsActive: true},
				{Domain: "test.com", IsActive: true},
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/domains", nil)
	h.ListDomains(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp model.HydraCollection
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.TotalItems != 2 {
		t.Errorf("totalItems = %d, want 2", resp.TotalItems)
	}
	if resp.Type != "hydra:Collection" {
		t.Errorf("type = %q, want %q", resp.Type, "hydra:Collection")
	}
	if resp.Context != "/contexts/Domain" {
		t.Errorf("context = %q, want %q", resp.Context, "/contexts/Domain")
	}
	if resp.ID != "/domains" {
		t.Errorf("id = %q, want %q", resp.ID, "/domains")
	}
}

func TestListDomains_Empty(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/domains", nil)
	h.ListDomains(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp model.HydraCollection
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.TotalItems != 0 {
		t.Errorf("totalItems = %d, want 0", resp.TotalItems)
	}
}

func TestListDomains_StoreError(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return nil, errors.New("db error")
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/domains", nil)
	h.ListDomains(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestListDomains_HydraFormat(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{
				{Domain: "mail.com", IsActive: true, CreatedAt: time.Now()},
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/domains", nil)
	h.ListDomains(c)

	var raw map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &raw)

	// Verify all Hydra fields are present
	for _, key := range []string{"@context", "@id", "@type", "hydra:totalItems", "hydra:member"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing Hydra field %q", key)
		}
	}
}

func TestListDomains_PrivateDomainHiddenByDefault(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{
				{Domain: "public.com", IsActive: true, IsPrivate: false},
				{Domain: "private.com", IsActive: true, IsPrivate: true},
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/domains", nil)
	h.ListDomains(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var member []model.Domain
	if err := json.Unmarshal(raw["hydra:member"], &member); err != nil {
		t.Fatalf("unmarshal member: %v", err)
	}
	if len(member) != 1 {
		t.Fatalf("member len = %d, want 1", len(member))
	}
	if member[0].Domain != "public.com" {
		t.Fatalf("domain=%q want %q", member[0].Domain, "public.com")
	}
}

func TestListDomains_PrivateDomainShownWithExplicitAuthorization(t *testing.T) {
	ms := &mockStore{
		listDomainsFunc: func(ctx context.Context) ([]model.Domain, error) {
			return []model.Domain{
				{Domain: "public.com", IsActive: true, IsPrivate: false},
				{Domain: "private.com", IsActive: true, IsPrivate: true},
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/domains", nil)
	// 模拟 wildcard API key，但显式授权 private.com（用于私有域名访问）。
	c.Set(middleware.CtxAPIKeyInfo, &middleware.APIKeyInfo{
		Name:      "Test",
		Domains:   []string{"*"},
		DomainSet: map[string]struct{}{"private.com": {}},
		Wildcard:  true,
	})
	c.Set(middleware.CtxAPIKeyDomains, []string{"*"})

	h.ListDomains(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var member []model.Domain
	if err := json.Unmarshal(raw["hydra:member"], &member); err != nil {
		t.Fatalf("unmarshal member: %v", err)
	}
	if len(member) != 2 {
		t.Fatalf("member len = %d, want 2", len(member))
	}
}
