package storage

import "errors"

const (
	// DefaultMaxFileBytes preserves the installation default while allowing the
	// runtime installation policy to choose a larger bounded value.
	DefaultMaxFileBytes int64 = 10 << 20
	// AbsoluteMaxFileBytes is the hard safety ceiling accepted by Aginex.
	AbsoluteMaxFileBytes int64 = 1 << 30

	defaultMaxPreviewBytes int64 = 32 << 20
	defaultMaxImageWidth         = 8192
	defaultMaxImageHeight        = 8192
	defaultMaxImagePixels  int64 = 40_000_000
)

var (
	ErrInvalidFilePolicy = errors.New("storage: invalid file policy")
	ErrFileSizeMismatch  = errors.New("storage: file size does not match upload intent")
)

// FilePolicy bounds arbitrary untrusted file streams. It intentionally has no
// MIME allowlist: browser names, extensions, and MIME values are only hints.
// Preview limits are fixed defensive bounds and are not installation settings.
type FilePolicy struct {
	MaxBytes        int64
	MaxPreviewBytes int64
	MaxImageWidth   int
	MaxImageHeight  int
	MaxImagePixels  int64
}

func DefaultFilePolicy() FilePolicy {
	policy, err := NewFilePolicy(DefaultMaxFileBytes)
	if err != nil {
		panic(err)
	}
	return policy
}

// NewFilePolicy constructs a runtime policy within the absolute 1 GiB limit.
func NewFilePolicy(maxBytes int64) (FilePolicy, error) {
	if maxBytes < 1 || maxBytes > AbsoluteMaxFileBytes {
		return FilePolicy{}, ErrInvalidFilePolicy
	}
	return FilePolicy{
		MaxBytes:        maxBytes,
		MaxPreviewBytes: min(maxBytes, defaultMaxPreviewBytes),
		MaxImageWidth:   defaultMaxImageWidth,
		MaxImageHeight:  defaultMaxImageHeight,
		MaxImagePixels:  defaultMaxImagePixels,
	}, nil
}

func (policy FilePolicy) Validate() error {
	if policy.MaxBytes < 1 || policy.MaxBytes > AbsoluteMaxFileBytes ||
		policy.MaxPreviewBytes < 1 || policy.MaxPreviewBytes > policy.MaxBytes ||
		policy.MaxImageWidth < 1 || policy.MaxImageHeight < 1 ||
		policy.MaxImagePixels < 1 {
		return ErrInvalidFilePolicy
	}
	return nil
}

func (policy FilePolicy) ValidateSize(size int64) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if size < 1 {
		return ErrEmptyContent
	}
	if size > policy.MaxBytes || size > AbsoluteMaxFileBytes {
		return ErrContentTooLarge
	}
	return nil
}
