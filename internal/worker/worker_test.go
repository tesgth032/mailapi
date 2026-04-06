package worker

import (
	"testing"

	"github.com/emersion/go-message/mail"
)

func TestGenerateIntro_Short(t *testing.T) {
	result := generateIntro("Hello world", 100)
	if result != "Hello world" {
		t.Errorf("got %q, want %q", result, "Hello world")
	}
}

func TestGenerateIntro_ExactLength(t *testing.T) {
	text := "abcdefghij" // 10 chars
	result := generateIntro(text, 10)
	if result != text {
		t.Errorf("got %q, want %q", result, text)
	}
}

func TestGenerateIntro_Truncated(t *testing.T) {
	text := "Hello this is a long message that should be truncated"
	result := generateIntro(text, 20)
	if len(result) > 23 { // 20 + "..."
		t.Errorf("result too long: %q (len %d)", result, len(result))
	}
	if result != "Hello this is a long..." {
		t.Errorf("got %q, want %q", result, "Hello this is a long...")
	}
}

func TestGenerateIntro_Empty(t *testing.T) {
	result := generateIntro("", 100)
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}

func TestGenerateIntro_WhitespaceNormalization(t *testing.T) {
	text := "  Hello   world  \n  foo   bar  "
	result := generateIntro(text, 100)
	if result != "Hello world foo bar" {
		t.Errorf("got %q, want %q", result, "Hello world foo bar")
	}
}

func TestGenerateIntro_OnlyWhitespace(t *testing.T) {
	result := generateIntro("   \n\t  ", 100)
	if result != "" {
		t.Errorf("got %q, want empty string", result)
	}
}

func TestGenerateIntro_ZeroMaxLen(t *testing.T) {
	result := generateIntro("Hello", 0)
	if result != "..." {
		t.Errorf("got %q, want %q", result, "...")
	}
}

func TestConvertAddress_Empty(t *testing.T) {
	result := convertAddress(nil)
	if result.Name != "" || result.Address != "" {
		t.Errorf("got %+v, want empty Address", result)
	}
}

func TestConvertAddress_Single(t *testing.T) {
	addrs := []*mail.Address{
		{Name: "John Doe", Address: "john@example.com"},
	}
	result := convertAddress(addrs)
	if result.Name != "John Doe" {
		t.Errorf("Name = %q, want %q", result.Name, "John Doe")
	}
	if result.Address != "john@example.com" {
		t.Errorf("Address = %q, want %q", result.Address, "john@example.com")
	}
}

func TestConvertAddress_Multiple(t *testing.T) {
	addrs := []*mail.Address{
		{Name: "First", Address: "first@example.com"},
		{Name: "Second", Address: "second@example.com"},
	}
	result := convertAddress(addrs)
	// Should only return the first address
	if result.Name != "First" {
		t.Errorf("Name = %q, want %q", result.Name, "First")
	}
}

func TestConvertAddresses_Empty(t *testing.T) {
	result := convertAddresses(nil)
	if len(result) != 0 {
		t.Errorf("got %d addresses, want 0", len(result))
	}
}

func TestConvertAddresses_Multiple(t *testing.T) {
	addrs := []*mail.Address{
		{Name: "Alice", Address: "alice@example.com"},
		{Name: "Bob", Address: "bob@example.com"},
		{Name: "", Address: "noreply@example.com"},
	}
	result := convertAddresses(addrs)
	if len(result) != 3 {
		t.Fatalf("got %d addresses, want 3", len(result))
	}
	if result[0].Name != "Alice" || result[0].Address != "alice@example.com" {
		t.Errorf("result[0] = %+v", result[0])
	}
	if result[1].Name != "Bob" || result[1].Address != "bob@example.com" {
		t.Errorf("result[1] = %+v", result[1])
	}
	if result[2].Name != "" || result[2].Address != "noreply@example.com" {
		t.Errorf("result[2] = %+v", result[2])
	}
}

func TestConvertAddress_NoName(t *testing.T) {
	addrs := []*mail.Address{
		{Address: "noreply@example.com"},
	}
	result := convertAddress(addrs)
	if result.Name != "" {
		t.Errorf("Name = %q, want empty", result.Name)
	}
	if result.Address != "noreply@example.com" {
		t.Errorf("Address = %q", result.Address)
	}
}
