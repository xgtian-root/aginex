package storage

import (
	"context"
	"io"
	"time"
)

// UploadRequest describes a bounded, short-lived object upload authorization.
type UploadRequest struct {
	Key         string
	ContentType string
	Size        int64
	Expires     time.Duration
}

// SignedRequest is a short-lived object storage operation. URL and headers are
// credentials and must never be logged, audited, or persisted in replay state.
type SignedRequest struct {
	URL       string            `json:"url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

// ObjectInfo contains metadata returned by the configured storage provider.
type ObjectInfo struct {
	Key         string    `json:"key"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
	ETag        string    `json:"etag"`
	ModifiedAt  time.Time `json:"modifiedAt"`
}

// ObjectStore is the public provider-neutral storage contract available to
// application modules. Cloud SDK types remain behind provider adapters.
type ObjectStore interface {
	CreateUpload(context.Context, UploadRequest) (SignedRequest, error)
	SignRead(context.Context, string, time.Duration) (SignedRequest, error)
	Stat(context.Context, string) (ObjectInfo, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

// ReadinessChecker verifies that the configured storage root or bucket is
// reachable without exposing object names or credentials.
type ReadinessChecker interface {
	CheckReadiness(context.Context) error
}
