package model

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestMessage_BSON_KeepFalseIsWritten(t *testing.T) {
	msg := Message{Keep: false}

	b, err := bson.Marshal(msg)
	if err != nil {
		t.Fatalf("bson.Marshal: %v", err)
	}

	var m bson.M
	if err := bson.Unmarshal(b, &m); err != nil {
		t.Fatalf("bson.Unmarshal: %v", err)
	}

	v, ok := m["keep"]
	if !ok {
		t.Fatalf("keep field missing in BSON (should be written even when false)")
	}

	keep, ok := v.(bool)
	if !ok {
		t.Fatalf("keep type=%T, want bool", v)
	}
	if keep != false {
		t.Fatalf("keep=%v, want false", keep)
	}
}
