package storage

import (
	"context"
	"errors"
	"io"
	"mime"
	"path"
	"strings"
	"time"

	frameworkstorage "github.com/xgtian-root/aginex/server/framework/storage"
)

type storageContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader storageContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

var (
	ErrInvalidReadDisposition = errors.New("invalid storage read disposition")
	ErrUnsafeInlineType       = errors.New("unsafe content type for inline preview")
	ErrInvalidFilename        = errors.New("invalid download filename")
	ErrInvalidMultipart       = errors.New("invalid multipart upload request")
	ErrMultipartNotFound      = errors.New("multipart upload not found")
)

var safeInlineTypes = map[string]struct{}{
	"image/jpeg":      {},
	"image/png":       {},
	"image/webp":      {},
	"image/gif":       {},
	"application/pdf": {},
}

func validateVerifiedPresentation(request VerifiedPresentationRequest) (string, error) {
	if err := ValidateKey(request.Key); err != nil {
		return "", err
	}
	contentType := strings.ToLower(strings.TrimSpace(request.ContentType))
	if contentType == StoredContentType {
		return contentType, nil
	}
	parsed, _, err := mime.ParseMediaType(contentType)
	if err != nil || parsed != contentType {
		return "", ErrUnsafeInlineType
	}
	if _, safe := safeInlineTypes[contentType]; !safe {
		return "", ErrUnsafeInlineType
	}
	return contentType, nil
}

func controlledReadValues(request ControlledReadRequest) (string, string, time.Duration, error) {
	if err := ValidateKey(request.Key); err != nil {
		return "", "", 0, err
	}
	contentType := StoredContentType
	switch request.Disposition {
	case ReadDispositionInline:
		parsed, _, err := mime.ParseMediaType(strings.TrimSpace(request.ContentType))
		if err != nil || parsed == "" || containsControl(parsed) {
			return "", "", 0, ErrUnsafeInlineType
		}
		contentType = strings.ToLower(parsed)
		if _, safe := safeInlineTypes[contentType]; !safe {
			return "", "", 0, ErrUnsafeInlineType
		}
	case ReadDispositionAttachment:
	default:
		return "", "", 0, ErrInvalidReadDisposition
	}

	dispositionValue, err := FormatContentDisposition(request.Disposition, request.Filename)
	if err != nil {
		return "", "", 0, err
	}
	expires := request.Expires
	if expires <= 0 {
		expires = 10 * time.Minute
	}
	return contentType, dispositionValue, expires, nil
}

// FormatContentDisposition emits both an ASCII filename fallback and the
// RFC 5987 filename* form. Local content handlers should call it only with the
// persisted original filename, never a query-string value.
func FormatContentDisposition(disposition ReadDisposition, filename string) (string, error) {
	if disposition != ReadDispositionInline && disposition != ReadDispositionAttachment {
		return "", ErrInvalidReadDisposition
	}
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "download"
	}
	if containsControl(filename) || strings.ContainsAny(filename, `/\`) ||
		path.Base(filename) != filename || filename == "." || filename == ".." {
		return "", ErrInvalidFilename
	}
	var fallback strings.Builder
	for _, character := range filename {
		if character >= 0x20 && character <= 0x7e {
			fallback.WriteRune(character)
		} else {
			fallback.WriteByte('_')
		}
	}
	base := mime.FormatMediaType(string(disposition), map[string]string{"filename": fallback.String()})
	if base == "" {
		return "", ErrInvalidFilename
	}
	return base + "; filename*=UTF-8''" + encodeRFC5987(filename), nil
}

func encodeRFC5987(value string) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	for _, character := range []byte(value) {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("!#$&+-.^_`|~", rune(character)) {
			encoded.WriteByte(character)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[character>>4])
		encoded.WriteByte(hexadecimal[character&0x0f])
	}
	return encoded.String()
}

func validateMultipartUpload(upload MultipartUpload) error {
	if err := ValidateKey(upload.Key); err != nil {
		return err
	}
	if strings.TrimSpace(upload.ProviderUploadID) == "" || containsControl(upload.ProviderUploadID) {
		return ErrInvalidMultipart
	}
	return nil
}

func validateMultipartPart(_ FilePolicy, request MultipartPartRequest) error {
	if err := validateMultipartUpload(request.Upload); err != nil {
		return err
	}
	if request.PartNumber < 1 || request.PartNumber > 10_000 {
		return ErrInvalidMultipart
	}
	return validateExistingTransferSize(request.Size)
}

func validateCompletion(request MultipartCompleteRequest) error {
	if err := validateMultipartUpload(request.Upload); err != nil {
		return err
	}
	if len(request.Parts) == 0 || len(request.Parts) > 10_000 {
		return ErrInvalidMultipart
	}
	previous := int32(0)
	for _, part := range request.Parts {
		if part.PartNumber <= previous || part.PartNumber > 10_000 || part.Size < 1 || strings.TrimSpace(part.ETag) == "" {
			return ErrInvalidMultipart
		}
		previous = part.PartNumber
	}
	return nil
}

func validateCompletionSize(_ FilePolicy, parts []UploadedPart) error {
	var total int64
	for _, part := range parts {
		if part.Size > AbsoluteMaxFileBytes-total {
			return ErrTooLarge
		}
		total += part.Size
	}
	return validateExistingTransferSize(total)
}

// Existing intents and multipart sessions retain the size accepted when they
// were created. A later restart with a lower runtime policy must not strand
// them, so data-plane continuation uses only the absolute safety ceiling.
func validateExistingTransferSize(size int64) error {
	if size < 1 {
		return frameworkstorage.ErrEmptyContent
	}
	if size > AbsoluteMaxFileBytes {
		return ErrTooLarge
	}
	return nil
}

func uploadedPartsMatch(acknowledged, provider []UploadedPart) bool {
	if len(acknowledged) != len(provider) {
		return false
	}
	providerByNumber := make(map[int32]UploadedPart, len(provider))
	for _, part := range provider {
		if part.PartNumber < 1 || part.Size < 1 || strings.TrimSpace(part.ETag) == "" {
			return false
		}
		if _, duplicate := providerByNumber[part.PartNumber]; duplicate {
			return false
		}
		providerByNumber[part.PartNumber] = part
	}
	for _, part := range acknowledged {
		actual, ok := providerByNumber[part.PartNumber]
		if !ok || actual.Size != part.Size || normalizeMultipartETag(actual.ETag) != normalizeMultipartETag(part.ETag) {
			return false
		}
	}
	return true
}

func normalizeMultipartETag(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "W/") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "W/"))
	}
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	return value
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
