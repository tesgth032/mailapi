package smtp

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"mailapi/internal/cache"
	"mailapi/internal/model"
	"mailapi/internal/queue"

	gosmtp "github.com/emersion/go-smtp"
)

// ListenerConfig represents a single SMTP listener binding.
type ListenerConfig struct {
	Addr    string   // "ip:port"
	Domains []string // email domains accepted on this listener (empty = all)
}

// Backend implements the go-smtp Backend interface.
type Backend struct {
	cache          cache.Interface
	queue          queue.Interface
	domain         string   // EHLO domain
	allowedDomains []string // 原始配置（用于日志）
	allowedSet     map[string]struct{}
	maxMessageBytes int64
}

// NewBackend creates a new SMTP backend.
// allowedDomains restricts which email domains this listener accepts.
// Pass nil to accept all domains (backward compatible behavior).
func NewBackend(c cache.Interface, q queue.Interface, domain string, allowedDomains []string, maxMessageBytes int64) *Backend {
	b := &Backend{cache: c, queue: q, domain: domain, allowedDomains: allowedDomains, maxMessageBytes: maxMessageBytes}
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
		return &gosmtp.SMTPError{
			Code:         550,
			EnhancedCode: gosmtp.EnhancedCode{5, 1, 1},
			Message:      fmt.Sprintf("User %s does not exist", addr),
		}
	}

	s.to = append(s.to, addr)
	return nil
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
