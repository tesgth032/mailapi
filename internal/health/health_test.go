package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type pingFn func(ctx context.Context) error

func (f pingFn) Ping(ctx context.Context) error { return f(ctx) }

type healthFn func(ctx context.Context) error

func (f healthFn) Health(ctx context.Context) error { return f(ctx) }

func TestHealthz(t *testing.T) {
	h := NewHealthz()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/healthz", nil)
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want %d", w.Code, http.StatusOK)
	}
}

func TestReadyz_OK(t *testing.T) {
	h := NewReadyz(ReadinessDeps{
		Store: pingFn(func(ctx context.Context) error { return nil }),
		Cache: pingFn(func(ctx context.Context) error { return nil }),
		Storage: healthFn(func(ctx context.Context) error {
			return nil
		}),
	}, Options{Timeout: 200 * time.Millisecond})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/readyz", nil)
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d want %d body=%q", w.Code, http.StatusOK, w.Body.String())
	}

	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["status"] != "ready" {
		t.Fatalf("status=%v want %q", out["status"], "ready")
	}
}

func TestReadyz_Fail(t *testing.T) {
	h := NewReadyz(ReadinessDeps{
		Store: pingFn(func(ctx context.Context) error { return errors.New("mongo down") }),
		Cache: pingFn(func(ctx context.Context) error { return nil }),
		Storage: healthFn(func(ctx context.Context) error {
			return nil
		}),
	}, Options{Timeout: 200 * time.Millisecond})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/readyz", nil)
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want %d body=%q", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}
}
