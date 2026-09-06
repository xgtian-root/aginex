package storage

import (
	"context"
	"io"
	"time"
)

const StoredContentType = "application/octet-stream"

// UploadRequest describes a bounded, short-lived object upload authorization.
type UploadRequest struct {
	Key         string
	ContentType string
	Size        int64
	Expires     time.Duration
	// Continuation re-authorizes a previously persisted intent after a policy
	// restart. New intents must leave it false and pass the current policy.
	Continuation bool
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
	Key                string    `json:"key"`
	Size               int64     `json:"size"`
	ContentType        string    `json:"contentType"`
	ContentDisposition string    `json:"contentDisposition"`
	ETag               string    `json:"etag"`
	ModifiedAt         time.Time `json:"modifiedAt"`
}

// ReadDisposition controls how a verified object is presented by a storage
// provider. Inline is deliberately limited by adapters to safe preview types;
// every other object must be downloaded as an attachment.
type ReadDisposition string

const (
	ReadDispositionInline     ReadDisposition = "inline"
	ReadDispositionAttachment ReadDisposition = "attachment"
)

// ControlledReadRequest describes response-header overrides for a short-lived
// read authorization. Filename is an untrusted display hint and is encoded by
// the provider adapter; callers must not build Content-Disposition themselves.
type ControlledReadRequest struct {
	Key         string
	Expires     time.Duration
	ContentType string
	Disposition ReadDisposition
	Filename    string
}

// ControlledRead is an optional storage capability. It lets an application
// force verified safe previews inline and all other objects to attachment
// without changing the immutable object metadata.
type ControlledRead interface {
	SignControlledRead(context.Context, ControlledReadRequest) (SignedRequest, error)
}

// VerifiedPresentationRequest describes provider metadata that may be applied
// only after the application has verified the stored bytes. ContentType must
// be either a safe inline preview type or StoredContentType. ExpectedETag
// prevents finalizing an object that changed after verification.
type VerifiedPresentationRequest struct {
	Key          string
	ContentType  string
	ExpectedETag string
}

// VerifiedPresentationFinalizer is an optional provider capability for stores
// whose signed reads cannot override Content-Type. Implementations do not
// persist a browser disposition after verification; every controlled read
// supplies inline or attachment explicitly on its short-lived authorization.
type VerifiedPresentationFinalizer interface {
	FinalizeVerifiedPresentation(context.Context, VerifiedPresentationRequest) (ObjectInfo, error)
}

// MultipartUpload is the private provider reference for an in-progress
// multipart upload. ProviderUploadID is sensitive operational state: it must
// never be logged, audited, or returned directly from an application API.
type MultipartUpload struct {
	Key              string
	ProviderUploadID string
}

// MultipartPartRequest describes one exact-size, short-lived part upload.
type MultipartPartRequest struct {
	Upload     MultipartUpload
	PartNumber int32
	Size       int64
	Expires    time.Duration
}

// UploadedPart is the provider-neutral inventory needed to verify and complete
// a multipart upload. ETag is opaque provider data and must be preserved
// exactly; it is not a whole-file checksum.
type UploadedPart struct {
	PartNumber int32
	Size       int64
	ETag       string
}

// MultipartCompleteRequest supplies the exact provider ETags acknowledged by
// the uploader and independently checked against ListUploadedParts.
type MultipartCompleteRequest struct {
	Upload MultipartUpload
	Parts  []UploadedPart
}

// MultipartObjectStore is an optional provider-neutral multipart capability.
// Application code persists the provider upload reference and part ETags; cloud
// SDK types remain behind adapters. Browser-facing cloud buckets must expose
// the UploadPart response ETag header through CORS so the application can ACK
// and persist it; CompleteMultipart rechecks the exact opaque values returned
// by ListUploadedParts before committing.
type MultipartObjectStore interface {
	InitiateMultipart(context.Context, UploadRequest) (MultipartUpload, error)
	SignUploadPart(context.Context, MultipartPartRequest) (SignedRequest, error)
	ListUploadedParts(context.Context, MultipartUpload) ([]UploadedPart, error)
	CompleteMultipart(context.Context, MultipartCompleteRequest) (ObjectInfo, error)
	AbortMultipart(context.Context, MultipartUpload) error
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
