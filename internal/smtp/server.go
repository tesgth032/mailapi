package smtp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mailapi/internal/cache"
	"mailapi/internal/model"
	"mailapi/internal/queue"
	"mailapi/internal/store"

	gosmtp "github.com/emersion/go-smtp"
	"golang.org/x/sync/singleflight"
)

// ListenerConfig represents a single SMTP listener binding.
type ListenerConfig struct {
	Addr    string   // "ip:port"
	Domains []string // email domains accepted on this listener (empty = all)
}

// Backend implements the go-smtp Backend interface.
type Backend struct {
	cache           cache.Interface
	queue           queue.Interface
	lookup          RecipientLookup
	accountTTL      time.Duration
	lookupSF        singleflight.Group
	lookupSem       chan struct{}
	negAddrCache    *ttlSet
	domain          string   // EHLO domain
	allowedDomains  []string // 原始配置（用于日志）
	allowedSet      map[string]struct{}
	maxMessageBytes int64
}

// RecipientLookup 是 SMTP 在 Redis miss 时用于回源验证地址存在性的最小接口。
// 主要目的：Redis flush/丢数据时不至于整站收不到信（自愈回灌）。
//
// 注意：该接口只用于“Redis miss”场景；正常路径仍然只依赖 Redis，避免把 Mongo 打爆。
type RecipientLookup interface {
	GetAccountByAddress(ctx context.Context, address string) (*model.Account, error)
}

// NewBackend creates a new SMTP backend.
// allowedDomains restricts which email domains this listener accepts.
// Pass nil to accept all domains (backward compatible behavior).
func NewBackend(c cache.Interface, q queue.Interface, lookup RecipientLookup, accountTTL time.Duration, domain string, allowedDomains []string, maxMessageBytes int64) *Backend {
	maxLookup := runtime.GOMAXPROCS(0) * 32
	if maxLookup < 32 {
		maxLookup = 32
	}
	if maxLookup > 256 {
		maxLookup = 256
	}

	b := &Backend{
		cache:           c,
		queue:           q,
		lookup:          lookup,
		accountTTL:      accountTTL,
		lookupSem:       make(chan struct{}, maxLookup),
		negAddrCache:    newTTLSet(200000, 30*time.Second),
		domain:          domain,
		allowedDomains:  allowedDomains,
		maxMessageBytes: maxMessageBytes,
	}
	if len(allowedDomains) > 0 {
		set := make(map[string]struct{}, len(allowedDomains))
		for _, d := range allowedDomains {
			set[strings.ToLower(d)] = struct{}{}
		}
		b.allowedSet = set
	}
	return b
}

func (b *Backend) NewSession(conn *gosmtp.Conn) (gosmtp.Session, error) {
	remoteAddr := conn.Conn().RemoteAddr().String()

	// SMTP-level rate limiting: 100 messages per minute per IP
	ip := remoteAddr
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil && strings.TrimSpace(host) != "" {
		ip = host
	}
	rlCtx, rlCancel := context.WithTimeout(context.Background(), 2*time.Second)
	allowed, err := b.cache.CheckSMTPRateLimit(rlCtx, ip, 100, time.Minute)
	rlCancel()
	if err != nil {
		log.Printf("SMTP rate limit check error: %v", err)
		// fail open
	} else if !allowed {
		return nil, &gosmtp.SMTPError{
			Code:         421,
			EnhancedCode: gosmtp.EnhancedCode{4, 7, 0},
			Message:      "Too many connections, try again later",
		}
	}

	return &Session{
		backend:    b,
		remoteAddr: remoteAddr,
	}, nil
}

// Session implements the go-smtp Session interface.
type Session struct {
	backend    *Backend
	from       string
	to         []string
	remoteAddr string
}

// SMTP 成功路径日志采样：避免高吞吐下日志本身成为 CPU/IO 热点。
const smtpSuccessLogSampleEvery = 100

var smtpSuccessLogCounter atomic.Uint64

func (s *Session) AuthPlain(username, password string) error {
	// Inbound SMTP does not require authentication
	return nil
}

func (s *Session) Mail(from string, opts *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *Session) Rcpt(to string, opts *gosmtp.RcptOptions) error {
	addr := strings.ToLower(to)

	// Check if domain is allowed on this listener
	if len(s.backend.allowedSet) > 0 {
		domain := domainFromAddress(addr)
		if _, ok := s.backend.allowedSet[domain]; !ok {
			return &gosmtp.SMTPError{
				Code:         550,
				EnhancedCode: gosmtp.EnhancedCode{5, 1, 1},
				Message:      "Domain not accepted on this server",
			}
		}
	}

	// Critical: validate recipient exists in Redis before accepting
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
	exists, err := s.backend.cache.HasAddress(checkCtx, addr)
	checkCancel()
	if err != nil {
		log.Printf("SMTP RCPT TO cache check error for %s: %v", addr, err)
		// On Redis failure, reject to be safe - prevents filling queue with undeliverable mail
		return &gosmtp.SMTPError{
			Code:         451,
			EnhancedCode: gosmtp.EnhancedCode{4, 3, 0},
			Message:      "Temporary service error, try again later",
		}
	}
	if !exists {
		// Redis miss 自愈：回源 Mongo（仅在 lookup 配置存在时），并把地址回写 Redis。
		// 目的：Redis flush/丢数据时不至于所有账号收不到信。
		if s.backend.lookup != nil {
			ok, fallbackErr := s.backend.lookupAndWarmAddress(addr)
			if fallbackErr != nil {
				log.Printf("SMTP RCPT TO fallback lookup error for %s: %v", addr, fallbackErr)
				return &gosmtp.SMTPError{
					Code:         451,
					EnhancedCode: gosmtp.EnhancedCode{4, 3, 0},
					Message:      "Temporary service error, try again later",
				}
			}
			if !ok {
				return &gosmtp.SMTPError{
					Code:         550,
					EnhancedCode: gosmtp.EnhancedCode{5, 1, 1},
					Message:      fmt.Sprintf("User %s does not exist", addr),
				}
			}
		} else {
			return &gosmtp.SMTPError{
				Code:         550,
				EnhancedCode: gosmtp.EnhancedCode{5, 1, 1},
				Message:      fmt.Sprintf("User %s does not exist", addr),
			}
		}
	}

	s.to = append(s.to, addr)
	return nil
}

func (b *Backend) lookupAndWarmAddress(addr string) (bool, error) {
	if b == nil || b.lookup == nil {
		return false, nil
	}
	addr = strings.ToLower(strings.TrimSpace(addr))
	if addr == "" {
		return false, nil
	}

	now := time.Now()
	if b.negAddrCache != nil && b.negAddrCache.Contains(now, addr) {
		return false, nil
	}

	// 并发上限：避免 Redis cache 冷启动/flush 时被大量随机收件人地址打爆 Mongo。
	select {
	case b.lookupSem <- struct{}{}:
		defer func() { <-b.lookupSem }()
	default:
		return false, fmt.Errorf("recipient lookup busy")
	}

	// 单 address 合并并发查询，优化“单 key 高并发”热点。
	ch := b.lookupSF.DoChan(addr, func() (any, error) {
		qCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		acc, err := b.lookup.GetAccountByAddress(qCtx, addr)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return lookupResult{exists: false}, nil
			}
			return lookupResult{exists: false}, err
		}

		ttl := time.Duration(0)
		if b.accountTTL > 0 && !acc.CreatedAt.IsZero() {
			exp := acc.CreatedAt.Add(b.accountTTL)
			if time.Now().After(exp) {
				// TTL 监控存在延迟：文档可能尚未被 Mongo 清理，但逻辑上已经过期，按不存在处理。
				return lookupResult{exists: false}, nil
			}
			ttl = time.Until(exp)
		}

		return lookupResult{exists: true, ttl: ttl}, nil
	})

	// 上层 Rcpt 已经给 Redis check 设过超时，这里再给 fallback 一个上限，避免 SMTP 会话长时间卡住。
	select {
	case res := <-ch:
		if res.Err != nil {
			return false, res.Err
		}
		r, ok := res.Val.(lookupResult)
		if !ok {
			return false, fmt.Errorf("unexpected lookup result type %T", res.Val)
		}
		if !r.exists {
			if b.negAddrCache != nil {
				b.negAddrCache.Add(now, addr, 30*time.Second)
			}
			return false, nil
		}

		// Best-effort warm: 写回 Redis，让后续 RCPT TO 走快路径。
		ttl := r.ttl
		if ttl <= 0 {
			ttl = b.accountTTL
		}
		if ttl > 0 {
			wCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = b.cache.SetAddress(wCtx, addr, ttl)
			cancel()
		}
		return true, nil
	case <-time.After(2 * time.Second):
		return false, fmt.Errorf("recipient lookup timeout")
	}
}

type lookupResult struct {
	exists bool
	ttl    time.Duration
}

type ttlSet struct {
	m           sync.Map // string -> int64(unixNano)
	size        atomic.Int64
	maxEntries  int64
	sweepEvery  time.Duration
	lastSweepNS atomic.Int64
}

func newTTLSet(maxEntries int64, sweepEvery time.Duration) *ttlSet {
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	if sweepEvery <= 0 {
		sweepEvery = 30 * time.Second
	}
	return &ttlSet{maxEntries: maxEntries, sweepEvery: sweepEvery}
}

func (s *ttlSet) Contains(now time.Time, key string) bool {
	if s == nil {
		return false
	}
	v, ok := s.m.Load(key)
	if !ok {
		return false
	}
	exp, ok := v.(int64)
	if !ok {
		s.m.Delete(key)
		return false
	}
	if exp <= now.UnixNano() {
		s.m.Delete(key)
		s.size.Add(-1)
		return false
	}
	return true
}

func (s *ttlSet) Add(now time.Time, key string, ttl time.Duration) {
	if s == nil || key == "" {
		return
	}
	if ttl <= 0 {
		ttl = time.Second
	}
	exp := now.Add(ttl).UnixNano()
	if _, loaded := s.m.LoadOrStore(key, exp); !loaded {
		s.size.Add(1)
	} else {
		s.m.Store(key, exp)
	}
	s.maybeSweep(now)
}

func (s *ttlSet) maybeSweep(now time.Time) {
	if s == nil || s.sweepEvery <= 0 {
		return
	}
	last := s.lastSweepNS.Load()
	nowNS := now.UnixNano()
	if last != 0 && nowNS-last < int64(s.sweepEvery) {
		return
	}
	if !s.lastSweepNS.CompareAndSwap(last, nowNS) {
		return
	}

	// 先清理过期项
	expired := int64(0)
	s.m.Range(func(k, v any) bool {
		exp, ok := v.(int64)
		if !ok || exp <= nowNS {
			s.m.Delete(k)
			expired++
		}
		return true
	})
	if expired > 0 {
		s.size.Add(-expired)
	}

	// 上限保护：如果仍然过大，随机/无序淘汰一部分（避免恶意/异常地址爆内存）。
	over := s.size.Load() - s.maxEntries
	if over <= 0 {
		return
	}
	removed := int64(0)
	s.m.Range(func(k, _ any) bool {
		if removed >= over {
			return false
		}
		s.m.Delete(k)
		removed++
		return true
	})
	if removed > 0 {
		s.size.Add(-removed)
	}
}

func (s *Session) Data(r io.Reader) error {
	// Read raw message with size limit (enforced by go-smtp MaxMessageBytes,
	// but we also guard here). Use configured maxMessageBytes (fallback 20MB).
	maxBytes := s.backend.maxMessageBytes
	if maxBytes <= 0 {
		maxBytes = 20 << 20
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1)) // +1 byte to detect overflow
	if err != nil {
		return &gosmtp.SMTPError{
			Code:         442,
			EnhancedCode: gosmtp.EnhancedCode{4, 3, 0},
			Message:      "Error reading message data",
		}
	}
	if int64(len(data)) > maxBytes {
		return &gosmtp.SMTPError{
			Code:         552,
			EnhancedCode: gosmtp.EnhancedCode{5, 3, 4},
			Message:      "Message size exceeds maximum",
		}
	}

	// Enqueue for async processing - don't parse here to keep SMTP fast
	email := &model.IncomingEmail{
		From:       s.from,
		To:         s.to,
		RawMessage: data,
		RemoteAddr: s.remoteAddr,
		ReceivedAt: time.Now().UnixMilli(),
	}

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = s.backend.queue.Publish(pubCtx, email)
	pubCancel()
	if err != nil {
		log.Printf("SMTP failed to enqueue message from %s: %v", s.from, err)
		return &gosmtp.SMTPError{
			Code:         451,
			EnhancedCode: gosmtp.EnhancedCode{4, 3, 0},
			Message:      "Temporary error, try again later",
		}
	}

	if smtpSuccessLogSampleEvery > 0 {
		n := smtpSuccessLogCounter.Add(1)
		if n%uint64(smtpSuccessLogSampleEvery) == 0 {
			log.Printf("SMTP accepted message from=%s to=%v size=%d", s.from, s.to, len(data))
		}
	}
	return nil
}

func (s *Session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *Session) Logout() error {
	return nil
}

// NewServer creates a configured go-smtp server for a single listener.
func NewServer(b *Backend, addr, domain string, maxMsgBytes int64, maxRecipients int, readTimeout, writeTimeout time.Duration) *gosmtp.Server {
	srv := gosmtp.NewServer(b)
	srv.Addr = addr
	srv.Domain = domain
	srv.MaxMessageBytes = maxMsgBytes
	srv.MaxRecipients = maxRecipients
	srv.ReadTimeout = readTimeout
	srv.WriteTimeout = writeTimeout
	srv.AllowInsecureAuth = true
	return srv
}

// BuildListeners derives SMTP listener configurations from domain-to-IP mappings.
// domainIPs maps each domain name to its list of listener IPs (empty = 0.0.0.0).
// defaultPort is used when the IP string doesn't include a port.
// Only returns listeners whose IPs are locally available (for distributed deployment,
// each node automatically discovers which listeners it should run).
func BuildListeners(domainIPs map[string][]string, defaultPort int) []ListenerConfig {
	// Invert: group domains by their listener address
	ipDomains := make(map[string][]string)
	for domain, ips := range domainIPs {
		if len(ips) == 0 {
			addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(defaultPort))
			ipDomains[addr] = append(ipDomains[addr], domain)
		} else {
			for _, ip := range ips {
				addr, err := normalizeListenAddr(ip, defaultPort)
				if err != nil {
					log.Printf("Invalid SMTP listener IP %q (domain=%s): %v", ip, domain, err)
					continue
				}
				ipDomains[addr] = append(ipDomains[addr], domain)
			}
		}
	}

	// Filter to locally-available IPs
	var listeners []ListenerConfig
	for addr, domains := range ipDomains {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			log.Printf("Invalid SMTP listener address %q: %v", addr, err)
			continue
		}
		if !IsLocalIP(host) {
			log.Printf("Skipping non-local SMTP listener %s (domains: %v)", addr, domains)
			continue
		}
		listeners = append(listeners, ListenerConfig{Addr: addr, Domains: domains})
	}
	return listeners
}

// IsLocalIP returns true if the given IP address is bound to a local network
// interface, or is a wildcard address (0.0.0.0, ::).
func IsLocalIP(ip string) bool {
	if ip == "0.0.0.0" || ip == "" || ip == "::" {
		return true
	}
	// loopback 一定是本地 IP（也避免每次都扫网卡地址）。
	if p := net.ParseIP(ip); p != nil && p.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if ok && ipNet.IP.String() == ip {
			return true
		}
	}
	return false
}

func domainFromAddress(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i != -1 && i+1 < len(addr) {
		return strings.ToLower(addr[i+1:])
	}
	return ""
}

func normalizeListenAddr(ip string, defaultPort int) (string, error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", fmt.Errorf("empty ip")
	}
	if defaultPort <= 0 || defaultPort > 65535 {
		return "", fmt.Errorf("invalid default port %d", defaultPort)
	}

	// 若包含端口：支持 "127.0.0.1:25" 与 "[::1]:25"。
	if host, port, err := net.SplitHostPort(ip); err == nil {
		if port == "" {
			return "", fmt.Errorf("missing port")
		}
		if _, err := strconv.Atoi(port); err != nil {
			return "", fmt.Errorf("invalid port %q", port)
		}
		return net.JoinHostPort(host, port), nil
	}

	// 纯 IP（无端口）：支持 IPv4/IPv6；允许写成 "[::1]"（无端口）。
	host := strings.TrimPrefix(ip, "[")
	host = strings.TrimSuffix(host, "]")
	if host != "" && net.ParseIP(host) == nil {
		return "", fmt.Errorf("invalid ip %q", ip)
	}

	return net.JoinHostPort(host, strconv.Itoa(defaultPort)), nil
}
