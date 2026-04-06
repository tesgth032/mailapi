package store

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestParseCursorToken_OK(t *testing.T) {
	const (
		ms  = int64(1710000000123)
		hex = "507f1f77bcf86cd799439011"
	)

	ts, id, ok := parseCursorToken("1710000000123.507f1f77bcf86cd799439011")
	if !ok {
		t.Fatalf("expected ok=true")
	}

	wantID, err := bson.ObjectIDFromHex(hex)
	if err != nil {
		t.Fatalf("ObjectIDFromHex: %v", err)
	}
	if id != wantID {
		t.Fatalf("id mismatch: got=%s want=%s", id.Hex(), wantID.Hex())
	}

	wantTS := time.UnixMilli(ms).UTC()
	if !ts.Equal(wantTS) {
		t.Fatalf("ts mismatch: got=%s want=%s", ts.Format(time.RFC3339Nano), wantTS.Format(time.RFC3339Nano))
	}
}

func TestParseCursorToken_NotToken(t *testing.T) {
	tests := []string{
		"",
		"507f1f77bcf86cd799439011",
		"abc.507f1f77bcf86cd799439011",
		"1710000000.invalid",
		".507f1f77bcf86cd799439011",
		"1710000000.",
		"1710000000.507f1f77bcf86cd799439011.extra",
	}

	for _, tc := range tests {
		_, _, ok := parseCursorToken(tc)
		if ok {
			t.Fatalf("expected ok=false for %q", tc)
		}
	}
}

