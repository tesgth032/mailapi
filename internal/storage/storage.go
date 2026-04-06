package storage

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Storage struct {
	client *minio.Client
	bucket string
}

const (
	rawAttachmentID = "_raw"
	rawFilename     = "message.eml"
	rawContentType  = "message/rfc822"
)

func New(ctx context.Context, endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Storage, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}

	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("create bucket: %w", err)
		}
	}

	return &Storage{client: client, bucket: bucket}, nil
}

// Health 用于 readiness 检查：验证对象存储可用性（轻量）。
func (s *Storage) Health(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("bucket %q does not exist", s.bucket)
	}
	return nil
}

// objectKey returns the MinIO object key for an attachment.
// Format: messages/{messageID}/{attachmentID}/{filename}
func objectKey(messageID, attachmentID, filename string) string {
	// 热路径：避免 fmt.Sprintf 的额外开销与分配。
	return "messages/" + messageID + "/" + attachmentID + "/" + filename
}

func (s *Storage) Upload(ctx context.Context, messageID, attachmentID, filename, contentType string, data []byte) error {
	return s.UploadReader(ctx, messageID, attachmentID, filename, contentType, bytes.NewReader(data), int64(len(data)))
}

func (s *Storage) UploadReader(ctx context.Context, messageID, attachmentID, filename, contentType string, r io.Reader, size int64) error {
	key := objectKey(messageID, attachmentID, filename)
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

func (s *Storage) Open(ctx context.Context, messageID, attachmentID, filename string) (io.ReadCloser, string, int64, error) {
	key := objectKey(messageID, attachmentID, filename)
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", 0, err
	}

	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, "", 0, err
	}

	return obj, info.ContentType, info.Size, nil
}

func (s *Storage) Download(ctx context.Context, messageID, attachmentID, filename string) ([]byte, string, error) {
	obj, contentType, _, err := s.Open(ctx, messageID, attachmentID, filename)
	if err != nil {
		return nil, "", err
	}
	defer obj.Close()

	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, "", err
	}

	return data, contentType, nil
}

func (s *Storage) GetPresignedURL(ctx context.Context, messageID, attachmentID, filename string, expiry time.Duration) (string, error) {
	key := objectKey(messageID, attachmentID, filename)
	reqParams := make(url.Values)
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, expiry, reqParams)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *Storage) DeleteByMessage(ctx context.Context, messageID string) error {
	// 热路径：避免 fmt.Sprintf。
	prefix := "messages/" + messageID + "/"
	// 派生 ctx，确保在删除失败时能尽快中止列举与删除，避免 goroutine/内部 channel 悬挂。
	delCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	objectCh := s.client.ListObjects(delCtx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	})

	// 批量删除：比逐个 RemoveObject 更少往返，尤其是邮件含多个附件时更明显。
	keysCh := make(chan minio.ObjectInfo, 128)
	listErrCh := make(chan error, 1)

	go func() {
		defer close(keysCh)
		for obj := range objectCh {
			if obj.Err != nil {
				listErrCh <- obj.Err
				return
			}
			select {
			case keysCh <- minio.ObjectInfo{Key: obj.Key}:
			case <-delCtx.Done():
				listErrCh <- delCtx.Err()
				return
			}
		}
		if err := delCtx.Err(); err != nil {
			listErrCh <- err
			return
		}
		listErrCh <- nil
	}()

	removeErrCh := s.client.RemoveObjects(delCtx, s.bucket, keysCh, minio.RemoveObjectsOptions{})
	var firstErr error
	for rmErr := range removeErrCh {
		if rmErr.Err != nil && firstErr == nil {
			firstErr = rmErr.Err
			// 尽快中止列举/删除，避免后续阻塞与资源占用。
			cancel()
		}
	}

	if err := <-listErrCh; err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Storage) ListMessageIDs(ctx context.Context, startAfter string, maxObjects int) (ids []string, nextStartAfter string, err error) {
	startAfter = strings.TrimSpace(startAfter)

	if maxObjects <= 0 {
		maxObjects = 20000
	}
	if maxObjects > 50000 {
		maxObjects = 50000
	}

	const prefix = "messages/"
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
		StartAfter: startAfter,
		MaxKeys:   maxObjects,
	}

	// 使用可取消 ctx，允许按 maxObjects “提前停止”扫描，避免全量扫 bucket。
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	objectCh := s.client.ListObjects(listCtx, s.bucket, opts)

	seen := make(map[string]struct{}, 1024)
	out := make([]string, 0, 1024)
	scanned := 0
	lastKey := startAfter

	for obj := range objectCh {
		if obj.Err != nil {
			return nil, startAfter, obj.Err
		}
		scanned++
		lastKey = obj.Key

		id, ok := messageIDFromObjectKey(obj.Key)
		if !ok {
			// 非预期 key：跳过。
			if scanned >= maxObjects {
				cancel()
			}
			continue
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			out = append(out, id)
		}

		if scanned >= maxObjects {
			// 提前停止：取消 list，继续 drain 直到 channel 关闭，避免 goroutine 泄露。
			cancel()
		}
	}

	// 扫描到尾部或无对象时，重置 cursor，形成“环形扫描”，避免新对象（key 更小）长期被跳过。
	if scanned < maxObjects {
		return out, "", nil
	}

	return out, lastKey, nil
}

func messageIDFromObjectKey(key string) (string, bool) {
	const prefix = "messages/"
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	rest := key[len(prefix):]
	i := strings.IndexByte(rest, '/')
	if i <= 0 {
		return "", false
	}
	id := rest[:i]
	if len(id) != 24 {
		return "", false
	}
	// 仅做轻量校验：24 位十六进制（Mongo ObjectID）。
	_, err := hex.DecodeString(id)
	return id, err == nil
}

func (s *Storage) UploadRawMessage(ctx context.Context, messageID string, data []byte) error {
	return s.Upload(ctx, messageID, rawAttachmentID, rawFilename, rawContentType, data)
}

func (s *Storage) OpenRawMessage(ctx context.Context, messageID string) (io.ReadCloser, string, int64, error) {
	return s.Open(ctx, messageID, rawAttachmentID, rawFilename)
}
