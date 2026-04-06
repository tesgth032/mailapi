package queue

import (
	"bytes"
	"encoding/json"
	"testing"

	"mailapi/internal/model"
)

func TestCodec_RoundTripBinary(t *testing.T) {
	in := &model.IncomingEmail{
		From:       "sender@example.com",
		To:         []string{"a@example.com", "b@example.com"},
		RawMessage: []byte("From: sender@example.com\r\nTo: a@example.com\r\n\r\nHello!"),
		RemoteAddr: "127.0.0.1:12345",
		ReceivedAt: 1710000000000,
	}

	data, err := encodeIncomingEmail(in)
	if err != nil {
		t.Fatalf("encodeIncomingEmail: %v", err)
	}

	var out model.IncomingEmail
	if err := decodeIncomingEmail(data, &out); err != nil {
		t.Fatalf("decodeIncomingEmail: %v", err)
	}

	if out.From != in.From {
		t.Fatalf("From=%q want %q", out.From, in.From)
	}
	if out.RemoteAddr != in.RemoteAddr {
		t.Fatalf("RemoteAddr=%q want %q", out.RemoteAddr, in.RemoteAddr)
	}
	if out.ReceivedAt != in.ReceivedAt {
		t.Fatalf("ReceivedAt=%d want %d", out.ReceivedAt, in.ReceivedAt)
	}
	if len(out.To) != len(in.To) {
		t.Fatalf("To len=%d want %d", len(out.To), len(in.To))
	}
	for i := range in.To {
		if out.To[i] != in.To[i] {
			t.Fatalf("To[%d]=%q want %q", i, out.To[i], in.To[i])
		}
	}
	if !bytes.Equal(out.RawMessage, in.RawMessage) {
		t.Fatalf("RawMessage mismatch")
	}
}

func TestCodec_JSONFallback(t *testing.T) {
	in := &model.IncomingEmail{
		From:       "sender@example.com",
		To:         []string{"x@example.com"},
		RawMessage: []byte("raw"),
		RemoteAddr: "10.0.0.1:25",
		ReceivedAt: 123,
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var out model.IncomingEmail
	if err := decodeIncomingEmail(data, &out); err != nil {
		t.Fatalf("decodeIncomingEmail(json): %v", err)
	}

	if out.From != in.From || out.RemoteAddr != in.RemoteAddr || out.ReceivedAt != in.ReceivedAt {
		t.Fatalf("basic fields mismatch")
	}
	if len(out.To) != 1 || out.To[0] != in.To[0] {
		t.Fatalf("To mismatch: %+v", out.To)
	}
	if !bytes.Equal(out.RawMessage, in.RawMessage) {
		t.Fatalf("RawMessage mismatch: got=%q want=%q", string(out.RawMessage), string(in.RawMessage))
	}
}

func TestCodec_InvalidPayload(t *testing.T) {
	var out model.IncomingEmail
	if err := decodeIncomingEmail([]byte("MAIL\x01\x01"), &out); err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

