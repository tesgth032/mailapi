package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"mailapi/internal/store"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestBulkUpdateMessages_ByIDs_Success(t *testing.T) {
	accountOID := bson.NewObjectID()
	id1 := bson.NewObjectID().Hex()
	id2 := bson.NewObjectID().Hex()

	var gotAccount bson.ObjectID
	var gotIDs []string
	var gotSeen *bool
	var gotKeep *bool

	ms := &mockStore{
		bulkUpdateMessageFlagsByIDsFunc: func(ctx context.Context, accountID bson.ObjectID, ids []string, seen, keep *bool) (int64, error) {
			gotAccount = accountID
			gotIDs = append([]string(nil), ids...)
			gotSeen = seen
			gotKeep = keep
			return 2, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	seen := true
	reqBody, _ := json.Marshal(map[string]any{
		"ids":  []string{id1, id2},
		"seen": seen,
	})

	c, w := newTestContext("PATCH", "/messages", reqBody)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.BulkUpdateMessages(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	if gotAccount != accountOID {
		t.Fatalf("accountID=%s, want %s", gotAccount.Hex(), accountOID.Hex())
	}
	if len(gotIDs) != 2 || gotIDs[0] != id1 || gotIDs[1] != id2 {
		t.Fatalf("ids=%v, want [%s %s]", gotIDs, id1, id2)
	}
	if gotSeen == nil || *gotSeen != true {
		t.Fatalf("seen=%v, want true", gotSeen)
	}
	if gotKeep != nil {
		t.Fatalf("keep=%v, want nil", *gotKeep)
	}
}

func TestBulkUpdateMessages_All_Success(t *testing.T) {
	accountOID := bson.NewObjectID()

	var gotAllAccount bson.ObjectID
	var gotSeen *bool

	ms := &mockStore{
		bulkUpdateMessageFlagsByAcctFunc: func(ctx context.Context, accountID bson.ObjectID, seen, keep *bool) (int64, error) {
			gotAllAccount = accountID
			gotSeen = seen
			return 0, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	seen := false
	reqBody, _ := json.Marshal(map[string]any{
		"all":  true,
		"seen": seen,
	})
	c, w := newTestContext("PATCH", "/messages", reqBody)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.BulkUpdateMessages(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if gotAllAccount != accountOID {
		t.Fatalf("accountID=%s, want %s", gotAllAccount.Hex(), accountOID.Hex())
	}
	if gotSeen == nil || *gotSeen != false {
		t.Fatalf("seen=%v, want false", gotSeen)
	}
}

func TestBulkUpdateMessages_Keep_Disabled(t *testing.T) {
	accountOID := bson.NewObjectID()
	h := newTestHandlerWithKeep(&mockStore{}, &mockCache{}, &mockStorage{}, false)

	reqBody := []byte(`{"ids":["` + bson.NewObjectID().Hex() + `"],"keep":true}`)
	c, w := newTestContext("PATCH", "/messages", reqBody)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.BulkUpdateMessages(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want %d; body=%s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestBulkUpdateMessages_InvalidID(t *testing.T) {
	accountOID := bson.NewObjectID()
	ms := &mockStore{
		bulkUpdateMessageFlagsByIDsFunc: func(ctx context.Context, accountID bson.ObjectID, ids []string, seen, keep *bool) (int64, error) {
			return 0, store.ErrInvalidID
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	reqBody := []byte(`{"ids":["not-an-objectid"],"seen":true}`)
	c, w := newTestContext("PATCH", "/messages", reqBody)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.BulkUpdateMessages(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func TestDeleteMessages_ByAccount_Success(t *testing.T) {
	accountOID := bson.NewObjectID()

	var gotAccount bson.ObjectID
	var gotSeen *bool
	var gotLimit int
	var gotDelta int64

	ms := &mockStore{
		softDeleteMessagesByAccountFunc: func(ctx context.Context, accountID bson.ObjectID, seen *bool, limit int) ([]string, int64, error) {
			gotAccount = accountID
			gotSeen = seen
			gotLimit = limit
			return []string{bson.NewObjectID().Hex()}, 123, nil
		},
		updateAccountUsedFunc: func(ctx context.Context, id bson.ObjectID, delta int64) error {
			gotDelta = delta
			return nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("DELETE", "/messages?seen=true&limit=2", nil)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.DeleteMessages(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if gotAccount != accountOID {
		t.Fatalf("accountID=%s, want %s", gotAccount.Hex(), accountOID.Hex())
	}
	if gotSeen == nil || *gotSeen != true {
		t.Fatalf("seen=%v, want true", gotSeen)
	}
	if gotLimit != 2 {
		t.Fatalf("limit=%d, want 2", gotLimit)
	}
	if gotDelta != -123 {
		t.Fatalf("delta=%d, want -123", gotDelta)
	}
}

func TestBulkDeleteMessagesByIDs_Success(t *testing.T) {
	accountOID := bson.NewObjectID()
	id1 := bson.NewObjectID().Hex()
	id2 := bson.NewObjectID().Hex()

	var gotAccount bson.ObjectID
	var gotIDs []string
	var gotDelta int64

	ms := &mockStore{
		softDeleteMessagesByIDsFunc: func(ctx context.Context, accountID bson.ObjectID, ids []string) ([]string, int64, error) {
			gotAccount = accountID
			gotIDs = append([]string(nil), ids...)
			return ids, 10, nil
		},
		updateAccountUsedFunc: func(ctx context.Context, id bson.ObjectID, delta int64) error {
			gotDelta = delta
			return nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	reqBody, _ := json.Marshal(map[string]any{"ids": []string{id1, id2}})
	c, w := newTestContext("POST", "/messages/bulk-delete", reqBody)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.BulkDeleteMessagesByIDs(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if gotAccount != accountOID {
		t.Fatalf("accountID=%s, want %s", gotAccount.Hex(), accountOID.Hex())
	}
	if len(gotIDs) != 2 || gotIDs[0] != id1 || gotIDs[1] != id2 {
		t.Fatalf("ids=%v, want [%s %s]", gotIDs, id1, id2)
	}
	if gotDelta != -10 {
		t.Fatalf("delta=%d, want -10", gotDelta)
	}
}
