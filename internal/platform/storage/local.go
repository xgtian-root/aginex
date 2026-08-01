package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type Local struct {
	root          string
	uploadBaseURL string
	readBaseURL   string
	policy        Policy
}

func NewLocal(root, uploadBaseURL, readBaseURL string, policy Policy) (*Local, error) {
	if root == "" {
		return nil, errors.New("local storage root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, err
	}
	return &Local{
		root: absolute, uploadBaseURL: uploadBaseURL, readBaseURL: readBaseURL, policy: policy,
	}, nil
}

func (l *Local) CreateUpload(_ context.Context, request UploadRequest) (SignedRequest, error) {
	if err := l.policy.Validate(request); err != nil {
		return SignedRequest{}, err
	}
	expires := request.Expires
	if expires <= 0 {
		expires = 10 * time.Minute
	}
	return SignedRequest{
		URL: l.objectURL(l.uploadBaseURL, request.Key), Method: http.MethodPut,
		Headers: map[string]string{"Content-Type": request.ContentType}, ExpiresAt: time.Now().UTC().Add(expires),
	}, nil
}

func (l *Local) CheckReadiness(ctx context.Context) error {
	if ctx == nil {
		return errors.New("local storage readiness context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	probe, err := os.CreateTemp(l.root, ".aginex-readiness-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(name)
	return errors.Join(closeErr, removeErr)
}

func (l *Local) SignRead(_ context.Context, key string, expires time.Duration) (SignedRequest, error) {
	if err := ValidateKey(key); err != nil {
		return SignedRequest{}, err
	}
	return SignedRequest{
		URL: l.objectURL(l.readBaseURL, key), Method: http.MethodGet,
		Headers: map[string]string{}, ExpiresAt: time.Now().UTC().Add(expires),
	}, nil
}

func (l *Local) Stat(_ context.Context, key string) (ObjectInfo, error) {
	objectPath, err := l.path(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	info, err := os.Stat(objectPath)
	if err != nil {
		return ObjectInfo{}, err
	}
	file, err := os.Open(objectPath)
	if err != nil {
		return ObjectInfo{}, err
	}
	defer file.Close()
	buffer := make([]byte, 512)
	read, _ := file.Read(buffer)
	return ObjectInfo{
		Key: key, Size: info.Size(), ContentType: http.DetectContentType(buffer[:read]), ModifiedAt: info.ModTime().UTC(),
	}, nil
}

func (l *Local) Delete(_ context.Context, key string) error {
	objectPath, err := l.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(objectPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (l *Local) Put(key string, body []byte, contentType string) error {
	if err := l.policy.Validate(UploadRequest{Key: key, Size: int64(len(body)), ContentType: contentType}); err != nil {
		return err
	}
	objectPath, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(objectPath), 0o750); err != nil {
		return err
	}
	return os.WriteFile(objectPath, body, 0o640)
}

func (l *Local) Open(_ context.Context, key string) (io.ReadCloser, error) {
	objectPath, err := l.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(objectPath)
}

func (l *Local) path(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	objectPath := filepath.Join(l.root, filepath.FromSlash(key))
	relative, err := filepath.Rel(l.root, objectPath)
	if err != nil || relative == ".." || filepath.IsAbs(relative) {
		return "", ErrInvalidKey
	}
	return objectPath, nil
}

func (l *Local) objectURL(baseURL, key string) string {
	return fmt.Sprintf("%s/%s", baseURL, url.PathEscape(key))
}
