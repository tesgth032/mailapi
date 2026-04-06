package debugserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"mailapi/internal/config"
	"mailapi/internal/health"
)

func TestServer_RoutesEnabled(t *testing.T) {
	srv, err := New(config.DebugServerConfig{
		Enabled: true,
		Host:    "127.0.0.1",
		Port:    6060,
		Metrics: true,
		Pprof:   true,
	}, health.ReadinessDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		path       string
		wantStatus int
	}{
		{path: "/healthz", wantStatus: http.StatusOK},
		{path: "/readyz", wantStatus: http.StatusOK},
		{path: "/metrics", wantStatus: http.StatusOK},
		{path: "/debug/pprof/", wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+tt.path, nil)
		srv.Handler.ServeHTTP(w, req)
		if w.Code != tt.wantStatus {
			t.Fatalf("path=%s status=%d want %d body=%q", tt.path, w.Code, tt.wantStatus, w.Body.String())
		}
	}
}

func TestServer_RoutesDisabled(t *testing.T) {
	srv, err := New(config.DebugServerConfig{
		Enabled: true,
		Host:    "127.0.0.1",
		Port:    6060,
		Metrics: false,
		Pprof:   false,
	}, health.ReadinessDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		path       string
		wantStatus int
	}{
		{path: "/metrics", wantStatus: http.StatusNotFound},
		{path: "/debug/pprof/", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+tt.path, nil)
		srv.Handler.ServeHTTP(w, req)
		if w.Code != tt.wantStatus {
			t.Fatalf("path=%s status=%d want %d body=%q", tt.path, w.Code, tt.wantStatus, w.Body.String())
		}
	}
}

func TestServer_RejectNonLoopback(t *testing.T) {
	_, err := New(config.DebugServerConfig{
		Enabled: true,
		Host:    "0.0.0.0",
		Port:    6060,
		Metrics: true,
		Pprof:   true,
	}, health.ReadinessDeps{})
	if err == nil {
		t.Fatalf("expected error")
	}
}
