package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load("/nonexistent/config.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.API.Host != "0.0.0.0" {
		t.Errorf("API Host = %q, want %q", cfg.Server.API.Host, "0.0.0.0")
	}
	if cfg.Server.API.Port != 8080 {
		t.Errorf("API Port = %d, want %d", cfg.Server.API.Port, 8080)
	}
	if cfg.Server.SMTP.Port != 25 {
		t.Errorf("SMTP Port = %d, want %d", cfg.Server.SMTP.Port, 25)
	}
	if cfg.Server.SMTP.Domain != "mail.example.com" {
		t.Errorf("SMTP Domain = %q, want %q", cfg.Server.SMTP.Domain, "mail.example.com")
	}
	if cfg.Server.SMTP.MaxMessageBytes != 20<<20 {
		t.Errorf("SMTP MaxMessageBytes = %d, want %d", cfg.Server.SMTP.MaxMessageBytes, 20<<20)
	}
	if cfg.MongoDB.URI != "mongodb://localhost:27017" {
		t.Errorf("MongoDB URI = %q, want default", cfg.MongoDB.URI)
	}
	if cfg.MongoDB.Database != "mailapi" {
		t.Errorf("MongoDB Database = %q, want %q", cfg.MongoDB.Database, "mailapi")
	}
	if cfg.Redis.Addr != "localhost:6379" {
		t.Errorf("Redis Addr = %q, want default", cfg.Redis.Addr)
	}
	if cfg.JWT.Secret != "change-me-to-a-random-secret" {
		t.Errorf("JWT Secret = %q, want default", cfg.JWT.Secret)
	}
	if cfg.JWT.Expiry != time.Hour {
		t.Errorf("JWT Expiry = %v, want %v", cfg.JWT.Expiry, time.Hour)
	}
	if cfg.Account.TTL != 168*time.Hour {
		t.Errorf("Account TTL = %v, want %v", cfg.Account.TTL, 168*time.Hour)
	}
	if cfg.Message.TTL != 168*time.Hour {
		t.Errorf("Message TTL = %v, want %v", cfg.Message.TTL, 168*time.Hour)
	}
}

func TestLoad_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
server:
  api:
    host: "127.0.0.1"
    port: 9090
  smtp:
    port: 2525
    domain: "test.example.com"
mongodb:
  uri: "mongodb://testhost:27017"
  database: "testdb"
redis:
  addr: "redis:6379"
  password: "secret"
  db: 2
jwt:
  secret: "my-test-secret"
  expiry: 2h
account:
  ttl: 48h
message:
  ttl: 24h
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.API.Host != "127.0.0.1" {
		t.Errorf("API Host = %q, want %q", cfg.Server.API.Host, "127.0.0.1")
	}
	if cfg.Server.API.Port != 9090 {
		t.Errorf("API Port = %d, want %d", cfg.Server.API.Port, 9090)
	}
	if cfg.Server.SMTP.Port != 2525 {
		t.Errorf("SMTP Port = %d, want %d", cfg.Server.SMTP.Port, 2525)
	}
	if cfg.Server.SMTP.Domain != "test.example.com" {
		t.Errorf("SMTP Domain = %q", cfg.Server.SMTP.Domain)
	}
	if cfg.MongoDB.URI != "mongodb://testhost:27017" {
		t.Errorf("MongoDB URI = %q", cfg.MongoDB.URI)
	}
	if cfg.MongoDB.Database != "testdb" {
		t.Errorf("MongoDB Database = %q", cfg.MongoDB.Database)
	}
	if cfg.Redis.Addr != "redis:6379" {
		t.Errorf("Redis Addr = %q", cfg.Redis.Addr)
	}
	if cfg.Redis.Password != "secret" {
		t.Errorf("Redis Password = %q", cfg.Redis.Password)
	}
	if cfg.Redis.DB != 2 {
		t.Errorf("Redis DB = %d", cfg.Redis.DB)
	}
	if cfg.JWT.Secret != "my-test-secret" {
		t.Errorf("JWT Secret = %q", cfg.JWT.Secret)
	}
	if cfg.JWT.Expiry != 2*time.Hour {
		t.Errorf("JWT Expiry = %v", cfg.JWT.Expiry)
	}
	if cfg.Account.TTL != 48*time.Hour {
		t.Errorf("Account TTL = %v", cfg.Account.TTL)
	}
	if cfg.Message.TTL != 24*time.Hour {
		t.Errorf("Message TTL = %v", cfg.Message.TTL)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("{{not yaml"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoad_PartialOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.yaml")
	content := `
jwt:
  secret: "override-only-this"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Overridden field
	if cfg.JWT.Secret != "override-only-this" {
		t.Errorf("JWT Secret = %q, want %q", cfg.JWT.Secret, "override-only-this")
	}
	// Defaults preserved for non-overridden fields
	if cfg.Server.API.Port != 8080 {
		t.Errorf("API Port = %d, want default 8080", cfg.Server.API.Port)
	}
	if cfg.MongoDB.URI != "mongodb://localhost:27017" {
		t.Errorf("MongoDB URI = %q, want default", cfg.MongoDB.URI)
	}
}
