package smtp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mailapi/internal/model"
	"mailapi/internal/queue"
	"mailapi/internal/store"

	gosmtp "github.com/emersion/go-smtp"
	"github.com/redis/go-redis/v9"
)

// --- Mock Cache for SMTP ---

type mockSMTPCache struct {
	pingFunc               func(ctx context.Context) error
	hasAddressFunc         func(ctx context.Context, addr string) (bool, error)
	checkSMTPRateLimitFunc func(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error)
	// Unused interface methods
	setAddressFunc     func(ctx context.Context, addr string, ttl time.Duration) error
	removeAddressFunc  func(ctx context.Context, addr string) error
	publishFunc        func(ctx context.Context, accountID string, payload string) error
	checkRateLimitFunc func(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error)
}

func (m *mockSMTPCache) Ping(ctx context.Context) error {
	if m.pingFunc != nil {
		return m.pingFunc(ctx)
	}
	return nil
}

func (m *mockSMTPCache) HasAddress(ctx context.Context, addr string) (bool, error) {
	if m.hasAddressFunc != nil {
		return m.hasAddressFunc(ctx, addr)
	}
	return false, nil
}

func (m *mockSMTPCache) CheckSMTPRateLimit(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error) {
	if m.checkSMTPRateLimitFunc != nil {
		return m.checkSMTPRateLimitFunc(ctx, ip, limit, window)
	}
	return true, nil
}

func (m *mockSMTPCache) SetAddress(ctx context.Context, addr string, ttl time.Duration) error {
	if m.setAddressFunc != nil {
		return m.setAddressFunc(ctx, addr, ttl)
	}
	return nil
}

func (m *mockSMTPCache) RemoveAddress(ctx context.Context, addr string) error {
	if m.removeAddressFunc != nil {
		return m.removeAddressFunc(ctx, addr)
	}
	return nil
}

func (m *mockSMTPCache) Publish(ctx context.Context, accountID string, payload string) error {
	if m.publishFunc != nil {
		return m.publishFunc(ctx, accountID, payload)
	}
	return nil
}

func (m *mockSMTPCache) Subscribe(_ context.Context, _ string) *redis.PubSub {
	return nil
}

func (m *mockSMTPCache) CheckRateLimit(ctx context.Context, ip string, limit int64, window time.Duration) (bool, error) {
	if m.checkRateLimitFunc != nil {
		return m.checkRateLimitFunc(ctx, ip, limit, window)
	}
	return true, nil
}

func (m *mockSMTPCache) CheckKeyRateLimit(_ context.Context, _ string, _ int64, _ time.Duration) (bool, error) {
	return true, nil
}

func (m *mockSMTPCache) CheckKeyDomainRateLimit(_ context.Context, _, _ string, _ int64, _ time.Duration) (bool, error) {
	return true, nil
}

func (m *mockSMTPCache) Client() *redis.Client { return nil }
func (m *mockSMTPCache) Close() error          { return nil }

// --- Mock Queue for SMTP ---

type mockSMTPQueue struct {
	publishFunc func(ctx context.Context, email *model.IncomingEmail) error
}

func (m *mockSMTPQueue) Publish(ctx context.Context, email *model.IncomingEmail) error {
	if m.publishFunc != nil {
		return m.publishFunc(ctx, email)
	}
	return nil
}

func (m *mockSMTPQueue) Consume(_ context.Context, _ queue.MessageHandler) error {
	return nil
}

func (m *mockSMTPQueue) Close() {}

// --- Tests ---

func newTestSession(cache *mockSMTPCache, queue *mockSMTPQueue) *Session {
	b := &Backend{
		cache:  cache,
		queue:  queue,
		domain: "test.example.com",
	}
	return &Session{
		backend:    b,
		remoteAddr: "127.0.0.1:12345",
	}
}

type mockRecipientLookup struct {
	getAccountByAddressFunc func(ctx context.Context, address string) (*model.Account, error)
}

func (m *mockRecipientLookup) GetAccountByAddress(ctx context.Context, address string) (*model.Account, error) {
	if m.getAccountByAddressFunc != nil {
		return m.getAccountByAddressFunc(ctx, address)
	}
	return nil, store.ErrNotFound
}

func TestSession_AuthPlain(t *testing.T) {
	s := newTestSession(&mockSMTPCache{}, &mockSMTPQueue{})
	if err := s.AuthPlain("user", "pass"); err != nil {
		t.Errorf("AuthPlain: %v", err)
	}
}

func TestSession_Mail(t *testing.T) {
	s := newTestSession(&mockSMTPCache{}, &mockSMTPQueue{})
	err := s.Mail("sender@example.com", &gosmtp.MailOptions{})
	if err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if s.from != "sender@example.com" {
		t.Errorf("from = %q, want %q", s.from, "sender@example.com")
	}
}

func TestSession_Rcpt_Success(t *testing.T) {
	mc := &mockSMTPCache{
		hasAddressFunc: func(ctx context.Context, addr string) (bool, error) {
			return true, nil
		},
	}
	s := newTestSession(mc, &mockSMTPQueue{})

	err := s.Rcpt("User@Example.COM", &gosmtp.RcptOptions{})
	if err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
	if len(s.to) != 1 {
		t.Fatalf("to has %d entries, want 1", len(s.to))
	}
	// Should be lowercased
	if s.to[0] != "user@example.com" {
		t.Errorf("to[0] = %q, want %q", s.to[0], "user@example.com")
	}
}

func TestSession_Rcpt_NotFound(t *testing.T) {
	mc := &mockSMTPCache{
		hasAddressFunc: func(ctx context.Context, addr string) (bool, error) {
			return false, nil
		},
	}
	s := newTestSession(mc, &mockSMTPQueue{})

	err := s.Rcpt("unknown@example.com", &gosmtp.RcptOptions{})
	if err == nil {
		t.Fatal("expected error for unknown recipient")
	}
	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T", err)
	}
	if smtpErr.Code != 550 {
		t.Errorf("SMTP code = %d, want 550", smtpErr.Code)
	}
}

func TestSession_Rcpt_CacheError(t *testing.T) {
	mc := &mockSMTPCache{
		hasAddressFunc: func(ctx context.Context, addr string) (bool, error) {
			return false, errors.New("redis error")
		},
	}
	s := newTestSession(mc, &mockSMTPQueue{})

	err := s.Rcpt("user@example.com", &gosmtp.RcptOptions{})
	if err == nil {
		t.Fatal("expected error on cache failure")
	}
	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T", err)
	}
	if smtpErr.Code != 451 {
		t.Errorf("SMTP code = %d, want 451", smtpErr.Code)
	}
}

func TestSession_Rcpt_RedisMiss_FallbackLookup_WarmsRedis(t *testing.T) {
	var warmedAddr string
	mc := &mockSMTPCache{
		hasAddressFunc: func(ctx context.Context, addr string) (bool, error) {
			return false, nil
		},
		setAddressFunc: func(ctx context.Context, addr string, ttl time.Duration) error {
			warmedAddr = addr
			return nil
		},
	}
	lookup := &mockRecipientLookup{
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			return &model.Account{Address: address, CreatedAt: time.Now().Add(-time.Hour)}, nil
		},
	}
	b := NewBackend(mc, &mockSMTPQueue{}, lookup, 24*time.Hour, false, "test.example.com", nil, 20<<20)
	s := &Session{backend: b, remoteAddr: "127.0.0.1:12345"}

	err := s.Rcpt("User@Example.COM", &gosmtp.RcptOptions{})
	if err != nil {
		t.Fatalf("Rcpt: %v", err)
	}
	if len(s.to) != 1 || s.to[0] != "user@example.com" {
		t.Fatalf("to=%v want [user@example.com]", s.to)
	}
	if warmedAddr != "user@example.com" {
		t.Fatalf("warmedAddr=%q want %q", warmedAddr, "user@example.com")
	}
}

func TestSession_Rcpt_RedisMiss_FallbackNotFound(t *testing.T) {
	mc := &mockSMTPCache{
		hasAddressFunc: func(ctx context.Context, addr string) (bool, error) {
			return false, nil
		},
	}
	lookup := &mockRecipientLookup{
		getAccountByAddressFunc: func(ctx context.Context, address string) (*model.Account, error) {
			return nil, store.ErrNotFound
		},
	}
	b := NewBackend(mc, &mockSMTPQueue{}, lookup, 24*time.Hour, false, "test.example.com", nil, 20<<20)
	s := &Session{backend: b, remoteAddr: "127.0.0.1:12345"}

	err := s.Rcpt("unknown@example.com", &gosmtp.RcptOptions{})
	if err == nil {
		t.Fatal("expected error for unknown recipient")
	}
	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T", err)
	}
	if smtpErr.Code != 550 {
		t.Errorf("SMTP code = %d, want 550", smtpErr.Code)
	}
}

func TestSession_Rcpt_MultipleRecipients(t *testing.T) {
	mc := &mockSMTPCache{
		hasAddressFunc: func(ctx context.Context, addr string) (bool, error) {
			return true, nil
		},
	}
	s := newTestSession(mc, &mockSMTPQueue{})

	_ = s.Rcpt("user1@example.com", &gosmtp.RcptOptions{})
	_ = s.Rcpt("user2@example.com", &gosmtp.RcptOptions{})

	if len(s.to) != 2 {
		t.Errorf("to has %d entries, want 2", len(s.to))
	}
}

func TestSession_Data_Success(t *testing.T) {
	mc := &mockSMTPCache{
		hasAddressFunc: func(ctx context.Context, addr string) (bool, error) {
			return true, nil
		},
	}
	var published *model.IncomingEmail
	mq := &mockSMTPQueue{
		publishFunc: func(ctx context.Context, email *model.IncomingEmail) error {
			published = email
			return nil
		},
	}
	s := newTestSession(mc, mq)
	s.from = "sender@example.com"
	s.to = []string{"user@example.com"}

	data := "From: sender@example.com\r\nTo: user@example.com\r\nSubject: Test\r\n\r\nHello!"
	err := s.Data(strings.NewReader(data))
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	if published == nil {
		t.Fatal("email was not published to queue")
	}
	if published.From != "sender@example.com" {
		t.Errorf("from = %q", published.From)
	}
	if len(published.To) != 1 || published.To[0] != "user@example.com" {
		t.Errorf("to = %v", published.To)
	}
	if string(published.RawMessage) != data {
		t.Errorf("rawMessage = %q", string(published.RawMessage))
	}
	if published.ReceivedAt == 0 {
		t.Error("receivedAt should be set")
	}
}

func TestNormalizeListenAddr(t *testing.T) {
	tests := []struct {
		in          string
		defaultPort int
		want        string
		wantErr     bool
	}{
		{in: "127.0.0.1", defaultPort: 25, want: "127.0.0.1:25"},
		{in: "127.0.0.1:2525", defaultPort: 25, want: "127.0.0.1:2525"},
		{in: "::1", defaultPort: 25, want: "[::1]:25"},
		{in: "[::1]:2525", defaultPort: 25, want: "[::1]:2525"},
		{in: "[::1]", defaultPort: 25, want: "[::1]:25"},
		{in: "  0.0.0.0  ", defaultPort: 2525, want: "0.0.0.0:2525"},
		// 注意：未加方括号的 "::1:2525" 是一个合法的 IPv6 地址（不是 host:port）。如需指定端口必须使用 "[addr]:port"。
		{in: "::1:2525", defaultPort: 25, want: "[::1:2525]:25"},
		{in: "not-an-ip", defaultPort: 25, wantErr: true},
	}

	for _, tc := range tests {
		got, err := normalizeListenAddr(tc.in, tc.defaultPort)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("in=%q expected error, got addr=%q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("in=%q err=%v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("in=%q got=%q want=%q", tc.in, got, tc.want)
		}
	}
}

func TestSession_Data_TooLarge(t *testing.T) {
	s := newTestSession(&mockSMTPCache{}, &mockSMTPQueue{})
	s.from = "sender@example.com"
	s.to = []string{"user@example.com"}

	// Create data larger than 20MB
	largeData := bytes.Repeat([]byte("x"), 20<<20+100)
	err := s.Data(bytes.NewReader(largeData))
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T", err)
	}
	if smtpErr.Code != 552 {
		t.Errorf("SMTP code = %d, want 552", smtpErr.Code)
	}
}

func TestSession_Data_QueueError(t *testing.T) {
	mq := &mockSMTPQueue{
		publishFunc: func(ctx context.Context, email *model.IncomingEmail) error {
			return errors.New("nats error")
		},
	}
	s := newTestSession(&mockSMTPCache{}, mq)
	s.from = "sender@example.com"
	s.to = []string{"user@example.com"}

	err := s.Data(strings.NewReader("Subject: test\r\n\r\nbody"))
	if err == nil {
		t.Fatal("expected error on queue publish failure")
	}
	var smtpErr *gosmtp.SMTPError
	if !errors.As(err, &smtpErr) {
		t.Fatalf("expected SMTPError, got %T", err)
	}
	if smtpErr.Code != 451 {
		t.Errorf("SMTP code = %d, want 451", smtpErr.Code)
	}
}

func TestSession_Reset(t *testing.T) {
	s := newTestSession(&mockSMTPCache{}, &mockSMTPQueue{})
	s.from = "sender@example.com"
	s.to = []string{"user@example.com"}

	s.Reset()

	if s.from != "" {
		t.Errorf("from = %q, want empty", s.from)
	}
	if s.to != nil {
		t.Errorf("to = %v, want nil", s.to)
	}
}

func TestSession_Logout(t *testing.T) {
	s := newTestSession(&mockSMTPCache{}, &mockSMTPQueue{})
	if err := s.Logout(); err != nil {
		t.Errorf("Logout: %v", err)
	}
}

func TestNewServer(t *testing.T) {
	b := &Backend{
		cache:  &mockSMTPCache{},
		queue:  &mockSMTPQueue{},
		domain: "test.example.com",
	}
	srv := NewServer(b, "0.0.0.0:2525", "test.example.com", 20<<20, 50, 60*time.Second, 60*time.Second, nil)
	if srv.Addr != "0.0.0.0:2525" {
		t.Errorf("Addr = %q, want %q", srv.Addr, "0.0.0.0:2525")
	}
	if srv.Domain != "test.example.com" {
		t.Errorf("Domain = %q", srv.Domain)
	}
	if srv.MaxMessageBytes != 20<<20 {
		t.Errorf("MaxMessageBytes = %d", srv.MaxMessageBytes)
	}
	if srv.MaxRecipients != 50 {
		t.Errorf("MaxRecipients = %d", srv.MaxRecipients)
	}
	if !srv.AllowInsecureAuth {
		t.Error("AllowInsecureAuth should be true")
	}
}

func TestSession_Data_RemoteAddrPreserved(t *testing.T) {
	var published *model.IncomingEmail
	mq := &mockSMTPQueue{
		publishFunc: func(ctx context.Context, email *model.IncomingEmail) error {
			published = email
			return nil
		},
	}
	s := newTestSession(&mockSMTPCache{}, mq)
	s.from = "sender@example.com"
	s.to = []string{"user@example.com"}
	s.remoteAddr = "192.168.1.100:54321"

	_ = s.Data(strings.NewReader("Subject: test\r\n\r\nbody"))

	if published.RemoteAddr != "192.168.1.100:54321" {
		t.Errorf("remoteAddr = %q, want %q", published.RemoteAddr, "192.168.1.100:54321")
	}
}
