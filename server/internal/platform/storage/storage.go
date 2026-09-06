package storage

import (
	"context"
	"errors"
	"net/url"
	"path"
	"strings"

	frameworkstorage "github.com/xgtian-root/aginex/server/framework/storage"
)

var (
	ErrInvalidKey        = errors.New("invalid object key")
	ErrBrowserUploadCORS = errors.New("browser upload CORS is not ready")
	// Deprecated compatibility aliases. Arbitrary file uploads no longer use a
	// MIME allowlist and size failures come from the shared FilePolicy.
	ErrInvalidType      = errors.New("unsupported content type")
	ErrTooLarge         = frameworkstorage.ErrContentTooLarge
	ErrFileSizeMismatch = frameworkstorage.ErrFileSizeMismatch
)

// BrowserUploadReadinessChecker verifies the anonymous CORS preflight used by
// direct browser uploads. Implementations must not upload an object or expose
// the signed probe URL in returned errors.
type BrowserUploadReadinessChecker interface {
	CheckBrowserUploadReadiness(context.Context, []string) error
}

type (
	UploadRequest                 = frameworkstorage.UploadRequest
	SignedRequest                 = frameworkstorage.SignedRequest
	ObjectInfo                    = frameworkstorage.ObjectInfo
	Storage                       = frameworkstorage.ObjectStore
	ReadinessChecker              = frameworkstorage.ReadinessChecker
	FilePolicy                    = frameworkstorage.FilePolicy
	ControlledRead                = frameworkstorage.ControlledRead
	ControlledReadRequest         = frameworkstorage.ControlledReadRequest
	VerifiedPresentationRequest   = frameworkstorage.VerifiedPresentationRequest
	VerifiedPresentationFinalizer = frameworkstorage.VerifiedPresentationFinalizer
	ReadDisposition               = frameworkstorage.ReadDisposition
	MultipartObjectStore          = frameworkstorage.MultipartObjectStore
	MultipartUpload               = frameworkstorage.MultipartUpload
	MultipartPartRequest          = frameworkstorage.MultipartPartRequest
	UploadedPart                  = frameworkstorage.UploadedPart
	MultipartCompleteRequest      = frameworkstorage.MultipartCompleteRequest
)

const (
	StoredContentType         = frameworkstorage.StoredContentType
	ReadDispositionInline     = frameworkstorage.ReadDispositionInline
	ReadDispositionAttachment = frameworkstorage.ReadDispositionAttachment
	DefaultMaxFileBytes       = frameworkstorage.DefaultMaxFileBytes
	AbsoluteMaxFileBytes      = frameworkstorage.AbsoluteMaxFileBytes
)

func DefaultFilePolicy() FilePolicy {
	return frameworkstorage.DefaultFilePolicy()
}

func NewFilePolicy(maxBytes int64) (FilePolicy, error) {
	return frameworkstorage.NewFilePolicy(maxBytes)
}

// DefaultImagePolicy remains as a source-compatible bridge for application
// code while the unreleased generic-file API is adopted.
func DefaultImagePolicy() FilePolicy {
	return DefaultFilePolicy()
}

func validateUpload(policy FilePolicy, request UploadRequest) error {
	if err := ValidateKey(request.Key); err != nil {
		return err
	}
	if request.Continuation {
		return validateExistingTransferSize(request.Size)
	}
	return policy.ValidateSize(request.Size)
}

func ValidateKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || containsControl(key) {
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
