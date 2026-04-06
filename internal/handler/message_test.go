package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"testing"

	"mailapi/internal/model"
	"mailapi/internal/store"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestListMessages_Success(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	ms := &mockStore{
		listMessagesFunc: func(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error) {
			return []model.Message{
				{ID: msgOID, AccountID: accountOID, Subject: "Hello"},
			}, 1, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages", nil)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.ListMessages(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp model.HydraCollection
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.TotalItems != 1 {
		t.Errorf("totalItems = %d, want 1", resp.TotalItems)
	}
}

func TestListMessages_Pagination(t *testing.T) {
	accountOID := bson.NewObjectID()
	var capturedPage, capturedPerPage int
	ms := &mockStore{
		listMessagesFunc: func(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error) {
			capturedPage = page
			capturedPerPage = perPage
			return []model.Message{}, 0, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages?page=3&itemsPerPage=10", nil)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.ListMessages(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if capturedPage != 3 {
		t.Errorf("page = %d, want 3", capturedPage)
	}
	if capturedPerPage != 10 {
		t.Errorf("perPage = %d, want 10", capturedPerPage)
	}
}

func TestListMessages_DefaultPagination(t *testing.T) {
	accountOID := bson.NewObjectID()
	var capturedPage, capturedPerPage int
	ms := &mockStore{
		listMessagesFunc: func(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error) {
			capturedPage = page
			capturedPerPage = perPage
			return []model.Message{}, 0, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages", nil)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.ListMessages(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if capturedPage != 1 {
		t.Errorf("default page = %d, want 1", capturedPage)
	}
	if capturedPerPage != 30 {
		t.Errorf("default perPage = %d, want 30", capturedPerPage)
	}
}

func TestListMessages_PerPageClamped(t *testing.T) {
	accountOID := bson.NewObjectID()
	var capturedPerPage int
	ms := &mockStore{
		listMessagesFunc: func(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error) {
			capturedPerPage = perPage
			return []model.Message{}, 0, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages?itemsPerPage=500", nil)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.ListMessages(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if capturedPerPage != 30 {
		t.Errorf("clamped perPage = %d, want 30 (>100 should reset)", capturedPerPage)
	}
}

func TestListMessages_DownloadURLSet(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	ms := &mockStore{
		listMessagesFunc: func(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error) {
			return []model.Message{
				{ID: msgOID, AccountID: accountOID, Subject: "Test"},
			}, 1, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages", nil)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.ListMessages(c)

	var raw map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &raw)

	var members []map[string]any
	json.Unmarshal(raw["hydra:member"], &members)
	if len(members) == 0 {
		t.Fatal("no members in response")
	}
	expected := fmt.Sprintf("/messages/%s/download", msgOID.Hex())
	if members[0]["downloadUrl"] != expected {
		t.Errorf("downloadUrl = %v, want %q", members[0]["downloadUrl"], expected)
	}
}

func TestListMessages_StoreError(t *testing.T) {
	accountOID := bson.NewObjectID()
	ms := &mockStore{
		listMessagesFunc: func(ctx context.Context, accountID bson.ObjectID, page, perPage int) ([]model.Message, int64, error) {
			return nil, 0, errors.New("db error")
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages", nil)
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.ListMessages(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGetMessage_Success(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	ms := &mockStore{
		getMessageFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: accountOID,
				Subject:   "Test Subject",
				Text:      "body text",
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages/"+msgOID.Hex(), nil)
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.GetMessage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["subject"] != "Test Subject" {
		t.Errorf("subject = %v", resp["subject"])
	}
}

func TestGetMessage_NotFound(t *testing.T) {
	ms := &mockStore{
		getMessageFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return nil, store.ErrNotFound
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages/abc", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	setAuth(c, bson.NewObjectID().Hex(), "user@example.com")
	h.GetMessage(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGetMessage_NotOwner(t *testing.T) {
	msgOID := bson.NewObjectID()
	otherAccountOID := bson.NewObjectID()
	ms := &mockStore{
		getMessageFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: otherAccountOID,
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages/"+msgOID.Hex(), nil)
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, bson.NewObjectID().Hex(), "user@example.com")
	h.GetMessage(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestUpdateMessage_Success(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	var updatedSeen bool
	ms := &mockStore{
		getMessageFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: accountOID,
				Subject:   "Test",
				Seen:      false,
			}, nil
		},
		updateMessageSeenFunc: func(ctx context.Context, id string, seen bool) error {
			updatedSeen = seen
			return nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"seen":true}`
	c, w := newTestContext("PATCH", "/messages/"+msgOID.Hex(), []byte(body))
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.UpdateMessage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if !updatedSeen {
		t.Error("expected seen to be updated to true")
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["seen"] != true {
		t.Errorf("response seen = %v, want true", resp["seen"])
	}
}

func TestUpdateMessage_Success_CombinedFlags(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()

	calls := 0
	var gotSeen bool
	var gotKeep bool
	var gotSeenSet bool
	var gotKeepSet bool

	ms := &mockStore{
		getMessageFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: accountOID,
				Subject:   "Test",
				Seen:      false,
				Keep:      false,
			}, nil
		},
		updateMessageFlagsFunc: func(ctx context.Context, id string, seen, keep *bool) error {
			calls++
			if seen != nil {
				gotSeen = *seen
				gotSeenSet = true
			}
			if keep != nil {
				gotKeep = *keep
				gotKeepSet = true
			}
			return nil
		},
	}
	h := newTestHandlerWithKeep(ms, &mockCache{}, &mockStorage{}, true)

	body := `{"seen":true,"keep":true}`
	c, w := newTestContext("PATCH", "/messages/"+msgOID.Hex(), []byte(body))
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.UpdateMessage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if calls != 1 {
		t.Fatalf("UpdateMessageFlags calls = %d, want 1", calls)
	}
	if !gotSeenSet || gotSeen != true {
		t.Errorf("seen set=%v val=%v, want set=true val=true", gotSeenSet, gotSeen)
	}
	if !gotKeepSet || gotKeep != true {
		t.Errorf("keep set=%v val=%v, want set=true val=true", gotKeepSet, gotKeep)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["seen"] != true {
		t.Errorf("response seen = %v, want true", resp["seen"])
	}
	if resp["keep"] != true {
		t.Errorf("response keep = %v, want true", resp["keep"])
	}
}

func TestUpdateMessage_NotOwner(t *testing.T) {
	msgOID := bson.NewObjectID()
	ms := &mockStore{
		getMessageFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: bson.NewObjectID(), // different owner
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"seen":true}`
	c, w := newTestContext("PATCH", "/messages/"+msgOID.Hex(), []byte(body))
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, bson.NewObjectID().Hex(), "user@example.com")
	h.UpdateMessage(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestUpdateMessage_NotFound(t *testing.T) {
	ms := &mockStore{
		getMessageFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return nil, store.ErrNotFound
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	body := `{"seen":true}`
	c, w := newTestContext("PATCH", "/messages/abc", []byte(body))
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	setAuth(c, bson.NewObjectID().Hex(), "user@example.com")
	h.UpdateMessage(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestUpdateMessage_Keep_Disabled(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	var keepCalled bool
	ms := &mockStore{
		getMessageFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: accountOID,
				Keep:      false,
			}, nil
		},
		updateMessageKeepFunc: func(ctx context.Context, id string, keep bool) error {
			keepCalled = true
			return nil
		},
	}
	h := newTestHandlerWithKeep(ms, &mockCache{}, &mockStorage{}, false)

	body := `{"keep":true}`
	c, w := newTestContext("PATCH", "/messages/"+msgOID.Hex(), []byte(body))
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.UpdateMessage(c)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
	if keepCalled {
		t.Error("expected keep not to be updated when disabled")
	}
}

func TestUpdateMessage_Keep_Enabled(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	var updatedKeep bool
	ms := &mockStore{
		getMessageFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: accountOID,
				Keep:      false,
			}, nil
		},
		updateMessageKeepFunc: func(ctx context.Context, id string, keep bool) error {
			updatedKeep = keep
			return nil
		},
	}
	h := newTestHandlerWithKeep(ms, &mockCache{}, &mockStorage{}, true)

	body := `{"keep":true}`
	c, w := newTestContext("PATCH", "/messages/"+msgOID.Hex(), []byte(body))
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.UpdateMessage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if !updatedKeep {
		t.Error("expected keep to be updated to true")
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["keep"] != true {
		t.Errorf("response keep = %v, want true", resp["keep"])
	}
}

func TestDeleteMessage_Success(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	var deleteCalled bool
	ms := &mockStore{
		getMessageMetaFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: accountOID,
			}, nil
		},
		deleteMessageFunc: func(ctx context.Context, id string) error {
			deleteCalled = true
			return nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	w := serveRequest(h, "DELETE", "/messages/:id", "/messages/"+msgOID.Hex(), nil, accountOID.Hex(), "user@example.com")

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNoContent)
	}
	if !deleteCalled {
		t.Error("store.DeleteMessage was not called")
	}
}

func TestDeleteMessage_NotOwner(t *testing.T) {
	msgOID := bson.NewObjectID()
	ms := &mockStore{
		getMessageMetaFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: bson.NewObjectID(),
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("DELETE", "/messages/"+msgOID.Hex(), nil)
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, bson.NewObjectID().Hex(), "user@example.com")
	h.DeleteMessage(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestDeleteMessage_NotFound(t *testing.T) {
	ms := &mockStore{
		getMessageMetaFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return nil, store.ErrNotFound
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("DELETE", "/messages/abc", nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	setAuth(c, bson.NewObjectID().Hex(), "user@example.com")
	h.DeleteMessage(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDownloadMessage_Success(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	rawContent := []byte("From: sender@example.com\r\nTo: user@example.com\r\n\r\nHello!")
	ms := &mockStore{
		getMessageRawFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:         msgOID,
				AccountID:  accountOID,
				RawMessage: rawContent,
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages/"+msgOID.Hex()+"/download", nil)
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.DownloadMessage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "message/rfc822" {
		t.Errorf("Content-Type = %q, want %q", ct, "message/rfc822")
	}
	if w.Body.String() != string(rawContent) {
		t.Errorf("body = %q", w.Body.String())
	}
	disp := w.Header().Get("Content-Disposition")
	mt, params, err := mime.ParseMediaType(disp)
	if err != nil {
		t.Fatalf("invalid Content-Disposition: %v (%q)", err, disp)
	}
	if mt != "attachment" {
		t.Errorf("Content-Disposition type = %q, want %q", mt, "attachment")
	}
	expected := fmt.Sprintf("%s.eml", msgOID.Hex())
	if params["filename"] != expected {
		t.Errorf("Content-Disposition filename = %q, want %q", params["filename"], expected)
	}
}

func TestDownloadMessage_NoRawMessage(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	ms := &mockStore{
		getMessageRawFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: accountOID,
				// RawMessage is nil
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages/"+msgOID.Hex()+"/download", nil)
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.DownloadMessage(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDownloadMessage_RawFromStorageFallback(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	rawContent := []byte("From: sender@example.com\r\nTo: user@example.com\r\n\r\nHello from storage!")

	ms := &mockStore{
		getMessageRawFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: accountOID,
				// RawMessage 为空，触发对象存储回退
			}, nil
		},
	}
	mst := &mockStorage{
		openRawMessageFunc: func(ctx context.Context, messageID string) (io.ReadCloser, string, int64, error) {
			return io.NopCloser(bytes.NewReader(rawContent)), "message/rfc822", int64(len(rawContent)), nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, mst)

	c, w := newTestContext("GET", "/messages/"+msgOID.Hex()+"/download", nil)
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.DownloadMessage(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "message/rfc822" {
		t.Errorf("Content-Type = %q, want %q", ct, "message/rfc822")
	}
	if w.Body.String() != string(rawContent) {
		t.Errorf("body = %q, want %q", w.Body.String(), string(rawContent))
	}
	disp := w.Header().Get("Content-Disposition")
	mt, params, err := mime.ParseMediaType(disp)
	if err != nil {
		t.Fatalf("invalid Content-Disposition: %v (%q)", err, disp)
	}
	if mt != "attachment" {
		t.Errorf("Content-Disposition type = %q, want %q", mt, "attachment")
	}
	expected := fmt.Sprintf("%s.eml", msgOID.Hex())
	if params["filename"] != expected {
		t.Errorf("Content-Disposition filename = %q, want %q", params["filename"], expected)
	}
}

func TestDownloadAttachment_Success(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	attID := bson.NewObjectID().Hex()
	attData := []byte("file-content")

	ms := &mockStore{
		getMessageMetaFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:             msgOID,
				AccountID:      accountOID,
				HasAttachments: true,
				Attachments: []model.Attachment{
					{ID: attID, Filename: "test.pdf", ContentType: "application/pdf", Size: 12},
				},
			}, nil
		},
	}
	mst := &mockStorage{
		openFunc: func(ctx context.Context, messageID, attachmentID, filename string) (io.ReadCloser, string, int64, error) {
			return io.NopCloser(bytes.NewReader(attData)), "application/pdf", int64(len(attData)), nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, mst)

	c, w := newTestContext("GET", fmt.Sprintf("/messages/%s/attachments/%s", msgOID.Hex(), attID), nil)
	c.Params = gin.Params{
		{Key: "id", Value: msgOID.Hex()},
		{Key: "attachmentId", Value: attID},
	}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.DownloadAttachment(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if w.Body.String() != "file-content" {
		t.Errorf("body = %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestDownloadAttachment_NotFound(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	ms := &mockStore{
		getMessageMetaFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:          msgOID,
				AccountID:   accountOID,
				Attachments: []model.Attachment{},
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", fmt.Sprintf("/messages/%s/attachments/nonexistent", msgOID.Hex()), nil)
	c.Params = gin.Params{
		{Key: "id", Value: msgOID.Hex()},
		{Key: "attachmentId", Value: "nonexistent"},
	}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.DownloadAttachment(c)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDownloadAttachment_StorageError(t *testing.T) {
	accountOID := bson.NewObjectID()
	msgOID := bson.NewObjectID()
	attID := bson.NewObjectID().Hex()
	ms := &mockStore{
		getMessageMetaFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: accountOID,
				Attachments: []model.Attachment{
					{ID: attID, Filename: "test.pdf"},
				},
			}, nil
		},
	}
	mst := &mockStorage{
		openFunc: func(ctx context.Context, messageID, attachmentID, filename string) (io.ReadCloser, string, int64, error) {
			return nil, "", 0, errors.New("storage error")
		},
	}
	h := newTestHandler(ms, &mockCache{}, mst)

	c, w := newTestContext("GET", fmt.Sprintf("/messages/%s/attachments/%s", msgOID.Hex(), attID), nil)
	c.Params = gin.Params{
		{Key: "id", Value: msgOID.Hex()},
		{Key: "attachmentId", Value: attID},
	}
	setAuth(c, accountOID.Hex(), "user@example.com")
	h.DownloadAttachment(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDownloadMessage_NotOwner(t *testing.T) {
	msgOID := bson.NewObjectID()
	ms := &mockStore{
		getMessageRawFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:         msgOID,
				AccountID:  bson.NewObjectID(),
				RawMessage: []byte("data"),
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", "/messages/"+msgOID.Hex()+"/download", nil)
	c.Params = gin.Params{{Key: "id", Value: msgOID.Hex()}}
	setAuth(c, bson.NewObjectID().Hex(), "user@example.com")
	h.DownloadMessage(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestDownloadAttachment_MessageNotOwner(t *testing.T) {
	msgOID := bson.NewObjectID()
	ms := &mockStore{
		getMessageMetaFunc: func(ctx context.Context, id string) (*model.Message, error) {
			return &model.Message{
				ID:        msgOID,
				AccountID: bson.NewObjectID(),
			}, nil
		},
	}
	h := newTestHandler(ms, &mockCache{}, &mockStorage{})

	c, w := newTestContext("GET", fmt.Sprintf("/messages/%s/attachments/att1", msgOID.Hex()), nil)
	c.Params = gin.Params{
		{Key: "id", Value: msgOID.Hex()},
		{Key: "attachmentId", Value: "att1"},
	}
	setAuth(c, bson.NewObjectID().Hex(), "user@example.com")
	h.DownloadAttachment(c)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}
