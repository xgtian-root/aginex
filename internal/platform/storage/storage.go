package storage

import (
	"errors"
	"net/url"
	"path"
	"strings"

	frameworkstorage "github.com/xgtian-root/aginex/framework/storage"
)

var (
	ErrInvalidKey  = errors.New("invalid object key")
	ErrInvalidType = errors.New("unsupported content type")
	ErrTooLarge    = errors.New("object exceeds the configured size limit")
)

var defaultImageTypes = map[string]struct{}{
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
}

type (
	UploadRequest    = frameworkstorage.UploadRequest
	SignedRequest    = frameworkstorage.SignedRequest
	ObjectInfo       = frameworkstorage.ObjectInfo
	Storage          = frameworkstorage.ObjectStore
	ReadinessChecker = frameworkstorage.ReadinessChecker
)

type Policy struct {
	MaxBytes     int64
	AllowedTypes map[string]struct{}
}

func DefaultImagePolicy() Policy {
	return Policy{MaxBytes: 10 << 20, AllowedTypes: defaultImageTypes}
}

func (p Policy) Validate(request UploadRequest) error {
	if err := ValidateKey(request.Key); err != nil {
		return err
	}
	if request.Size < 1 || (p.MaxBytes > 0 && request.Size > p.MaxBytes) {
		return ErrTooLarge
	}
	allowed := p.AllowedTypes
	if len(allowed) == 0 {
		allowed = defaultImageTypes
	}
	if _, ok := allowed[strings.ToLower(request.ContentType)]; !ok {
		return ErrInvalidType
	}
	return nil
}

func ValidateKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return ErrInvalidKey
	}
	clean := path.Clean(key)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != key {
		return ErrInvalidKey
	}
	decoded, err := url.PathUnescape(key)
	if err != nil || decoded != key {
		return ErrInvalidKey
	}
	return nil
}
