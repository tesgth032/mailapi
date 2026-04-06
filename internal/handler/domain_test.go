package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

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
