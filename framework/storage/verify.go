// Package storage provides provider-neutral object storage lifecycle
// primitives. It does not depend on a cloud SDK or a database dialect.
package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"strings"

	_ "golang.org/x/image/webp"
)

var (
	// ErrInvalidPolicy means the verifier was configured without bounded,
	// supported limits.
	ErrInvalidPolicy = errors.New("storage: invalid image verification policy")
	// ErrEmptyContent means the input contained no bytes.
	ErrEmptyContent = errors.New("storage: empty content")
	// ErrContentTooLarge means the input exceeded Policy.MaxBytes.
	ErrContentTooLarge = errors.New("storage: content exceeds the configured size limit")
	// ErrUnsupportedMIMEType means the claimed or detected content type is not
	// enabled by the policy.
	ErrUnsupportedMIMEType = errors.New("storage: unsupported MIME type")
	// ErrMIMETypeMismatch means the claimed content type differs from the
	// signature detected from the content.
	ErrMIMETypeMismatch = errors.New("storage: claimed MIME type does not match content")
	// ErrMalformedImage means the content has an image signature but is not a
	// complete, decodable image.
	ErrMalformedImage = errors.New("storage: malformed image")
	// ErrImageDimensionsExceeded means either dimension exceeds its configured
	// bound.
	ErrImageDimensionsExceeded = errors.New("storage: image dimensions exceed the configured limit")
	// ErrImagePixelsExceeded means width multiplied by height exceeds the
	// configured decoded-pixel bound.
	ErrImagePixelsExceeded = errors.New("storage: image pixels exceed the configured limit")
)

const (
	MIMEJPEG = "image/jpeg"
	MIMEPNG  = "image/png"
	MIMEWebP = "image/webp"
)

var supportedImageMIMETypes = map[string]struct{}{
	MIMEJPEG: {},
	MIMEPNG:  {},
	MIMEWebP: {},
}

// Policy bounds an untrusted image before it is fully decoded.
type Policy struct {
	MaxBytes         int64
	MaxWidth         int
	MaxHeight        int
	MaxPixels        int64
	AllowedMIMETypes []string
}

// DefaultImagePolicy permits JPEG, PNG, and WebP images up to 10 MiB, 8192
// pixels on either axis, and 40 megapixels.
func DefaultImagePolicy() Policy {
	return Policy{
		MaxBytes:         10 << 20,
		MaxWidth:         8192,
		MaxHeight:        8192,
		MaxPixels:        40_000_000,
		AllowedMIMETypes: []string{MIMEJPEG, MIMEPNG, MIMEWebP},
	}
}

// VerifiedImage is metadata derived from verified bytes. SHA256 is a
// lower-case hexadecimal digest.
type VerifiedImage struct {
	MIMEType string
	Size     int64
	SHA256   string
	Width    int
	Height   int
}

// ImageVerifier verifies untrusted image streams using immutable policy state.
type ImageVerifier struct {
	policy  Policy
	allowed map[string]struct{}
}

// NewImageVerifier validates and clones policy so callers cannot change a
// verifier's accepted types after construction.
func NewImageVerifier(policy Policy) (*ImageVerifier, error) {
	maxAddressableBytes := int64(^uint(0) >> 1)
	if policy.MaxBytes <= 0 ||
		policy.MaxBytes >= maxAddressableBytes ||
		policy.MaxWidth <= 0 ||
		policy.MaxHeight <= 0 ||
		policy.MaxPixels <= 0 ||
		len(policy.AllowedMIMETypes) == 0 {
		return nil, ErrInvalidPolicy
	}

	allowed := make(map[string]struct{}, len(policy.AllowedMIMETypes))
	for _, value := range policy.AllowedMIMETypes {
		normalized, err := normalizeMIMEType(value)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrInvalidPolicy, value)
		}
		if _, supported := supportedImageMIMETypes[normalized]; !supported {
			return nil, fmt.Errorf("%w: %q", ErrInvalidPolicy, value)
		}
		allowed[normalized] = struct{}{}
	}

	cloned := policy
	cloned.AllowedMIMETypes = append([]string(nil), policy.AllowedMIMETypes...)
	return &ImageVerifier{policy: cloned, allowed: allowed}, nil
}

// Verify reads at most MaxBytes+1 bytes, validates the claimed and detected
// MIME types, checks dimensions before allocating the decoded image, fully
// decodes the image, and returns metadata derived only from its content.
func (v *ImageVerifier) Verify(
	ctx context.Context,
	reader io.Reader,
	claimedMIMEType string,
) (VerifiedImage, error) {
	if v == nil || reader == nil {
		return VerifiedImage{}, ErrInvalidPolicy
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return VerifiedImage{}, err
	}

	claimed, err := normalizeMIMEType(claimedMIMEType)
	if err != nil {
		return VerifiedImage{}, fmt.Errorf("%w: %q", ErrUnsupportedMIMEType, claimedMIMEType)
	}
	if _, ok := v.allowed[claimed]; !ok {
		return VerifiedImage{}, fmt.Errorf("%w: %s", ErrUnsupportedMIMEType, claimed)
	}

	data, digest, err := readBounded(ctx, reader, v.policy.MaxBytes)
	if err != nil {
		return VerifiedImage{}, err
	}

	detected, err := detectImageMIMEType(data)
	if err != nil {
		return VerifiedImage{}, err
	}
	if _, ok := v.allowed[detected]; !ok {
		return VerifiedImage{}, fmt.Errorf("%w: %s", ErrUnsupportedMIMEType, detected)
	}
	if claimed != detected {
		return VerifiedImage{}, fmt.Errorf(
			"%w: claimed %s, detected %s",
			ErrMIMETypeMismatch,
			claimed,
			detected,
		)
	}
	if err := validateImageContainer(detected, data); err != nil {
		return VerifiedImage{}, fmt.Errorf("%w: %v", ErrMalformedImage, err)
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return VerifiedImage{}, fmt.Errorf("%w: decode config: %v", ErrMalformedImage, err)
	}
	decodedMIMEType, ok := mimeTypeForFormat(format)
	if !ok || decodedMIMEType != detected {
		return VerifiedImage{}, fmt.Errorf(
			"%w: signature %s decoded as %q",
			ErrMalformedImage,
			detected,
			format,
		)
	}
	if config.Width < 1 || config.Height < 1 {
		return VerifiedImage{}, fmt.Errorf(
			"%w: invalid dimensions %dx%d",
			ErrMalformedImage,
			config.Width,
			config.Height,
		)
	}
	if config.Width > v.policy.MaxWidth || config.Height > v.policy.MaxHeight {
		return VerifiedImage{}, fmt.Errorf(
			"%w: got %dx%d, maximum %dx%d",
			ErrImageDimensionsExceeded,
			config.Width,
			config.Height,
			v.policy.MaxWidth,
			v.policy.MaxHeight,
		)
	}
	pixels := uint64(config.Width) * uint64(config.Height)
	if pixels > uint64(v.policy.MaxPixels) {
		return VerifiedImage{}, fmt.Errorf(
			"%w: got %d, maximum %d",
			ErrImagePixelsExceeded,
			pixels,
			v.policy.MaxPixels,
		)
	}

	decoded, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return VerifiedImage{}, fmt.Errorf("%w: decode: %v", ErrMalformedImage, err)
	}
	bounds := decoded.Bounds()
	if decodedFormat != format || bounds.Dx() != config.Width || bounds.Dy() != config.Height {
		return VerifiedImage{}, fmt.Errorf(
			"%w: inconsistent decoded image metadata",
			ErrMalformedImage,
		)
	}

	return VerifiedImage{
		MIMEType: detected,
		Size:     int64(len(data)),
		SHA256:   digest,
		Width:    config.Width,
		Height:   config.Height,
	}, nil
}

func readBounded(
	ctx context.Context,
	reader io.Reader,
	maxBytes int64,
) ([]byte, string, error) {
	limit := maxBytes + 1
	var content bytes.Buffer
	hash := sha256.New()
	written, err := io.Copy(
		io.MultiWriter(&content, hash),
		io.LimitReader(contextReader{ctx: ctx, reader: reader}, limit),
	)
	if err != nil {
		return nil, "", fmt.Errorf("storage: read content: %w", err)
	}
	if written == 0 {
		return nil, "", ErrEmptyContent
	}
	if written > maxBytes {
		return nil, "", ErrContentTooLarge
	}
	return content.Bytes(), hex.EncodeToString(hash.Sum(nil)), nil
}

func normalizeMIMEType(value string) (string, error) {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil || mediaType == "" {
		return "", ErrUnsupportedMIMEType
	}
	return strings.ToLower(mediaType), nil
}

func detectImageMIMEType(data []byte) (string, error) {
	if len(data) == 0 {
		return "", ErrEmptyContent
	}
	detected := http.DetectContentType(data)
	if _, ok := supportedImageMIMETypes[detected]; !ok {
		return "", fmt.Errorf("%w: detected %s", ErrUnsupportedMIMEType, detected)
	}
	return detected, nil
}

func mimeTypeForFormat(format string) (string, bool) {
	switch format {
	case "jpeg":
		return MIMEJPEG, true
	case "png":
		return MIMEPNG, true
	case "webp":
		return MIMEWebP, true
	default:
		return "", false
	}
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
