package cfworker

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewProxy_UpstreamNotConfigured(t *testing.T) {
	h := NewProxy(Config{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://cfworker.api.mailapi.com/api/mails?limit=1", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusServiceUnavailable)
	}
	if got := rr.Body.String(); got == "" {
		t.Fatalf("expected non-empty body")
	}
}

func TestNewProxy_ForwardsRequestAndResponse(t *testing.T) {
	var gotPath, gotQuery, gotMethod string
	var gotAuth, gotAdmin, gotUser, gotCustom string
	var gotConn, gotUpgrade string
	var gotXFH, gotXFF, gotXFP string
	var gotBody []byte

	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		gotAdmin = r.Header.Get("x-admin-auth")
		gotUser = r.Header.Get("x-user-token")
		gotCustom = r.Header.Get("x-custom-auth")
		gotConn = r.Header.Get("Connection")
		gotUpgrade = r.Header.Get("Upgrade")
		gotXFH = r.Header.Get("X-Forwarded-Host")
		gotXFF = r.Header.Get("X-Forwarded-For")
		gotXFP = r.Header.Get("X-Forwarded-Proto")

		b, _ := io.ReadAll(r.Body)
		gotBody = b

		// 返回一些关键头，确保代理不丢失
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=\"x.json\"")
		w.Header().Set("X-Upstream", "ok")
		// hop-by-hop（不应被客户端看到）
		w.Header().Set("Connection", "close")

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer up.Close()

	h := NewProxy(Config{Upstream: up.URL, Timeout: 2 * time.Second})

	body := bytes.Repeat([]byte("a"), 64*1024)
	req := httptest.NewRequest(http.MethodPost, "http://cfworker.api.mailapi.com/api/mails?limit=10&offset=20", bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.9:12345"
	req.Header.Set("Authorization", "Bearer test-jwt")
	req.Header.Set("x-admin-auth", "admin-pass")
	req.Header.Set("x-user-token", "user-jwt")
	req.Header.Set("x-custom-auth", "custom-pass")
	req.Header.Set("X-Test", "1")
	req.Header.Set("Connection", "Upgrade, Foo")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Foo", "bar") // 应被 Connection 列表移除

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// --- upstream 侧断言 ---
	if gotPath != "/api/mails" {
		t.Fatalf("upstream path=%q want %q", gotPath, "/api/mails")
	}
	if gotQuery != "limit=10&offset=20" {
		t.Fatalf("upstream query=%q want %q", gotQuery, "limit=10&offset=20")
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("upstream method=%q want %q", gotMethod, http.MethodPost)
	}
	if gotAuth != "Bearer test-jwt" {
		t.Fatalf("Authorization=%q want %q", gotAuth, "Bearer test-jwt")
	}
	if gotAdmin != "admin-pass" {
		t.Fatalf("x-admin-auth=%q want %q", gotAdmin, "admin-pass")
	}
	if gotUser != "user-jwt" {
		t.Fatalf("x-user-token=%q want %q", gotUser, "user-jwt")
	}
	if gotCustom != "custom-pass" {
		t.Fatalf("x-custom-auth=%q want %q", gotCustom, "custom-pass")
	}
	if gotConn != "" || gotUpgrade != "" {
		t.Fatalf("hop-by-hop headers should be removed: Connection=%q Upgrade=%q", gotConn, gotUpgrade)
	}
	if gotXFH != "cfworker.api.mailapi.com" {
		t.Fatalf("X-Forwarded-Host=%q want %q", gotXFH, "cfworker.api.mailapi.com")
	}
	if gotXFP != "http" {
		t.Fatalf("X-Forwarded-Proto=%q want %q", gotXFP, "http")
	}
	if gotXFF == "" {
		t.Fatalf("expected X-Forwarded-For to be set")
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("upstream body mismatch")
	}

	// --- client 侧断言 ---
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d want %d", rr.Code, http.StatusCreated)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type=%q want %q", ct, "application/json")
	}
	if cd := rr.Header().Get("Content-Disposition"); cd == "" {
		t.Fatalf("expected Content-Disposition to be forwarded")
	}
	if rr.Header().Get("X-Upstream") != "ok" {
		t.Fatalf("expected X-Upstream header")
	}
	if rr.Header().Get("Connection") != "" {
		t.Fatalf("response hop-by-hop header should be removed, got Connection=%q", rr.Header().Get("Connection"))
	}
	if rr.Body.String() != `{"ok":true}` {
		t.Fatalf("body=%q want %q", rr.Body.String(), `{"ok":true}`)
	}
}

