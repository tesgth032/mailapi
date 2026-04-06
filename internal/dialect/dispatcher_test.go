package dialect_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"mailapi/internal/dialect"
)

func textHandler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	})
}

func perform(d *dialect.Dispatcher, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://example.invalid/health", nil)
	req.Host = host
	w := httptest.NewRecorder()
	d.ServeHTTP(w, req)
	return w
}

func TestDispatcher_BaseHostEmpty_DisabledAlwaysDefault(t *testing.T) {
	d := dialect.NewDispatcher("", "duck", nil, "reject")
	d.Register("duck", textHandler("duck"))
	d.Register("cfworker", textHandler("cfworker"))

	for _, host := range []string{
		"api.mailapi.com",
		"cfworker.api.mailapi.com",
		"localhost:8080",
		"127.0.0.1:8080",
	} {
		w := perform(d, host)
		if w.Code != http.StatusOK {
			t.Fatalf("host=%q: status=%d, want=%d", host, w.Code, http.StatusOK)
		}
		if got := w.Body.String(); got != "duck" {
			t.Fatalf("host=%q: body=%q, want=%q", host, got, "duck")
		}
	}
}

func TestDispatcher_HostEqualsBaseHost_UsesDefaultDialect(t *testing.T) {
	d := dialect.NewDispatcher("api.mailapi.com", "duck", nil, "reject")
	d.Register("duck", textHandler("duck"))

	for _, host := range []string{"api.mailapi.com", "api.mailapi.com:8080"} {
		w := perform(d, host)
		if w.Code != http.StatusOK {
			t.Fatalf("host=%q: status=%d, want=%d", host, w.Code, http.StatusOK)
		}
		if got := w.Body.String(); got != "duck" {
			t.Fatalf("host=%q: body=%q, want=%q", host, got, "duck")
		}
	}
}

func TestDispatcher_ExplicitDialect_SubdomainPrefix(t *testing.T) {
	d := dialect.NewDispatcher("api.mailapi.com", "duck", nil, "reject")
	d.Register("duck", textHandler("duck"))
	d.Register("cfworker", textHandler("cfworker"))

	w := perform(d, "cfworker.api.mailapi.com")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d", w.Code, http.StatusOK)
	}
	if got := w.Body.String(); got != "cfworker" {
		t.Fatalf("body=%q, want=%q", got, "cfworker")
	}
}

func TestDispatcher_HostNotMatchBaseHost_ForcesDefaultDialect(t *testing.T) {
	d := dialect.NewDispatcher("api.mailapi.com", "duck", nil, "reject")
	d.Register("duck", textHandler("duck"))
	d.Register("cfworker", textHandler("cfworker"))

	for _, host := range []string{"localhost:8080", "127.0.0.1:8080"} {
		w := perform(d, host)
		if w.Code != http.StatusOK {
			t.Fatalf("host=%q: status=%d, want=%d", host, w.Code, http.StatusOK)
		}
		if got := w.Body.String(); got != "duck" {
			t.Fatalf("host=%q: body=%q, want=%q", host, got, "duck")
		}
	}
}

func TestDispatcher_MultiLevelSubdomain_TreatedAsUnknown(t *testing.T) {
	// reject: a.b.<baseHost> 视为未知 dialect
	{
		d := dialect.NewDispatcher("api.mailapi.com", "duck", nil, "reject")
		d.Register("duck", textHandler("duck"))
		d.Register("cfworker", textHandler("cfworker"))

		w := perform(d, "a.b.api.mailapi.com")
		if w.Code != http.StatusNotFound {
			t.Fatalf("reject: status=%d, want=%d", w.Code, http.StatusNotFound)
		}
	}

	// fallback: a.b.<baseHost> 回退到默认 dialect
	{
		d := dialect.NewDispatcher("api.mailapi.com", "duck", nil, "fallback")
		d.Register("duck", textHandler("duck"))
		d.Register("cfworker", textHandler("cfworker"))

		w := perform(d, "a.b.api.mailapi.com")
		if w.Code != http.StatusOK {
			t.Fatalf("fallback: status=%d, want=%d", w.Code, http.StatusOK)
		}
		if got := w.Body.String(); got != "duck" {
			t.Fatalf("fallback: body=%q, want=%q", got, "duck")
		}
	}
}

func TestDispatcher_UnknownDialectName_Strategy(t *testing.T) {
	// reject: typo.<baseHost> 且未注册 -> 404
	{
		d := dialect.NewDispatcher("api.mailapi.com", "duck", nil, "reject")
		d.Register("duck", textHandler("duck"))

		w := perform(d, "typo.api.mailapi.com")
		if w.Code != http.StatusNotFound {
			t.Fatalf("reject: status=%d, want=%d", w.Code, http.StatusNotFound)
		}
	}

	// fallback: typo.<baseHost> 且未注册 -> 回退默认
	{
		d := dialect.NewDispatcher("api.mailapi.com", "duck", nil, "fallback")
		d.Register("duck", textHandler("duck"))

		w := perform(d, "typo.api.mailapi.com")
		if w.Code != http.StatusOK {
			t.Fatalf("fallback: status=%d, want=%d", w.Code, http.StatusOK)
		}
		if got := w.Body.String(); got != "duck" {
			t.Fatalf("fallback: body=%q, want=%q", got, "duck")
		}
	}
}

func TestDispatcher_EnabledDialects_RestrictsRouting(t *testing.T) {
	// enabledDialects 只允许 duck：即使注册了 cfworker，也应按 unknownDialect 处理。
	{
		d := dialect.NewDispatcher("api.mailapi.com", "duck", []string{"duck"}, "reject")
		d.Register("duck", textHandler("duck"))
		d.Register("cfworker", textHandler("cfworker"))

		w := perform(d, "cfworker.api.mailapi.com")
		if w.Code != http.StatusNotFound {
			t.Fatalf("reject: status=%d, want=%d", w.Code, http.StatusNotFound)
		}
	}

	{
		d := dialect.NewDispatcher("api.mailapi.com", "duck", []string{"duck"}, "fallback")
		d.Register("duck", textHandler("duck"))
		d.Register("cfworker", textHandler("cfworker"))

		w := perform(d, "cfworker.api.mailapi.com")
		if w.Code != http.StatusOK {
			t.Fatalf("fallback: status=%d, want=%d", w.Code, http.StatusOK)
		}
		if got := w.Body.String(); got != "duck" {
			t.Fatalf("fallback: body=%q, want=%q", got, "duck")
		}
	}

	// 显式请求 duck.<baseHost> 且在 enabled 列表中，应正常路由到 duck。
	{
		d := dialect.NewDispatcher("api.mailapi.com", "duck", []string{"duck"}, "reject")
		d.Register("duck", textHandler("duck"))

		w := perform(d, "duck.api.mailapi.com")
		if w.Code != http.StatusOK {
			t.Fatalf("duck enabled: status=%d, want=%d", w.Code, http.StatusOK)
		}
		if got := w.Body.String(); got != "duck" {
			t.Fatalf("duck enabled: body=%q, want=%q", got, "duck")
		}
	}
}

