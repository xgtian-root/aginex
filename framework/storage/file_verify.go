package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	_ "golang.org/x/image/webp"
)

const fileInspectionTailBytes = 1 << 20

// PreviewKind is derived only from verified file bytes. Unknown and malformed
// formats remain valid arbitrary files, but are never presented inline.
type PreviewKind string

const (
	PreviewNone  PreviewKind = "none"
	PreviewImage PreviewKind = "image"
	PreviewPDF   PreviewKind = "pdf"
)

// VerifiedFile contains metadata derived from the complete untrusted stream.
// SHA256 is a lower-case hexadecimal digest of the exact uploaded bytes.
type VerifiedFile struct {
	MIMEType    string
	Size        int64
	SHA256      string
	Width       int
	Height      int
	PreviewKind PreviewKind
}

// FileVerifier verifies arbitrary files without imposing a MIME allowlist.
// Invalid image or PDF structures are retained as downloads and deliberately
// downgraded to application/octet-stream with no preview.
type FileVerifier struct {
	policy FilePolicy
}

func NewFileVerifier(policy FilePolicy) (*FileVerifier, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &FileVerifier{policy: policy}, nil
}

// Verify consumes the complete stream once. Only policy/size/read failures
// reject the file; malformed preview candidates degrade to an opaque download.
func (verifier *FileVerifier) Verify(
	ctx context.Context,
	reader io.Reader,
	expectedSize int64,
) (VerifiedFile, error) {
	if verifier == nil || reader == nil {
		return VerifiedFile{}, ErrInvalidFilePolicy
	}
	if err := verifier.policy.ValidateSize(expectedSize); err != nil {
		return VerifiedFile{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return VerifiedFile{}, err
	}

	inspection := newFileInspection(verifier.policy.MaxPreviewBytes)
	digest := sha256.New()
	written, err := io.Copy(
		io.MultiWriter(digest, inspection),
		io.LimitReader(contextReader{ctx: ctx, reader: reader}, verifier.policy.MaxBytes+1),
	)
	if err != nil {
		return VerifiedFile{}, fmt.Errorf("storage: read content: %w", err)
	}
	if written == 0 {
		return VerifiedFile{}, ErrEmptyContent
	}
	if written > verifier.policy.MaxBytes || written > AbsoluteMaxFileBytes {
		return VerifiedFile{}, ErrContentTooLarge
	}
	if written != expectedSize {
		return VerifiedFile{}, fmt.Errorf(
			"%w: expected %d bytes, received %d",
			ErrFileSizeMismatch,
			expectedSize,
			written,
		)
	}

	detected := http.DetectContentType(inspection.prefix)
	verified := VerifiedFile{
		MIMEType:    detected,
		Size:        written,
		SHA256:      hex.EncodeToString(digest.Sum(nil)),
		PreviewKind: PreviewNone,
	}

	switch detected {
	case MIMEJPEG, MIMEPNG, MIMEWebP, MIMEGIF:
		data, ok := inspection.complete()
		if !ok {
			return downgradePreview(verified), nil
		}
		width, height, ok := verifier.verifyImage(detected, data)
		if !ok {
			return downgradePreview(verified), nil
		}
		verified.Width = width
		verified.Height = height
		verified.PreviewKind = PreviewImage
	case MIMEPDF:
		data, ok := inspection.complete()
		if !ok || !validatePDFStructure(data) {
			return downgradePreview(verified), nil
		}
		verified.PreviewKind = PreviewPDF
	}
	return verified, nil
}

func (verifier *FileVerifier) verifyImage(mimeType string, data []byte) (int, int, bool) {
	if err := validateImageContainer(mimeType, data); err != nil {
		return 0, 0, false
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, false
	}
	decodedMIMEType, ok := mimeTypeForFormat(format)
	if !ok || decodedMIMEType != mimeType || config.Width < 1 || config.Height < 1 {
		return 0, 0, false
	}
	if config.Width > verifier.policy.MaxImageWidth ||
		config.Height > verifier.policy.MaxImageHeight {
		return 0, 0, false
	}
	pixels := uint64(config.Width) * uint64(config.Height)
	if pixels > uint64(verifier.policy.MaxImagePixels) {
		return 0, 0, false
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format {
		return 0, 0, false
	}
	decodedBounds := decoded.Bounds()
	if decodedBounds.Dx() != config.Width || decodedBounds.Dy() != config.Height {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}

func downgradePreview(file VerifiedFile) VerifiedFile {
	file.MIMEType = StoredContentType
	file.Width = 0
	file.Height = 0
	file.PreviewKind = PreviewNone
	return file
}

type fileInspection struct {
	prefix     []byte
	tail       []byte
	data       []byte
	previewMax int64
	truncated  bool
}

func newFileInspection(previewMax int64) *fileInspection {
	return &fileInspection{
		prefix:     make([]byte, 0, 512),
		tail:       make([]byte, 0, min(previewMax, fileInspectionTailBytes)),
		data:       make([]byte, 0, min(previewMax, 64<<10)),
		previewMax: previewMax,
	}
}

func (inspection *fileInspection) Write(data []byte) (int, error) {
	if remaining := 512 - len(inspection.prefix); remaining > 0 {
		inspection.prefix = append(inspection.prefix, data[:min(len(data), remaining)]...)
	}

	inspection.tail = append(inspection.tail, data...)
	if overflow := len(inspection.tail) - fileInspectionTailBytes; overflow > 0 {
		copy(inspection.tail, inspection.tail[overflow:])
		inspection.tail = inspection.tail[:len(inspection.tail)-overflow]
	}

	if !inspection.truncated {
		if int64(len(inspection.data))+int64(len(data)) <= inspection.previewMax {
			inspection.data = append(inspection.data, data...)
		} else {
			inspection.data = nil
			inspection.truncated = true
		}
	}
	return len(data), nil
}

func (inspection *fileInspection) complete() ([]byte, bool) {
	if inspection == nil || inspection.truncated {
		return nil, false
	}
	return inspection.data, true
}

var (
	pdfRootReferencePattern = regexp.MustCompile(`/Root\s+([0-9]+)\s+([0-9]+)\s+R(?:\s|[>/])`)
	pdfCatalogPattern       = regexp.MustCompile(`/Type\s*/Catalog(?:\s|[>/])`)
	pdfXRefStreamPattern    = regexp.MustCompile(`^\s*([0-9]+)\s+([0-9]+)\s+obj(?:\s|<)`)
	pdfXRefTypePattern      = regexp.MustCompile(`/Type\s*/XRef(?:\s|[>/])`)
	pdfXRefSubsection       = regexp.MustCompile(`(?m)^[0-9]+[ \t]+[1-9][0-9]*[ \t]*\r?$`)
	pdfXRefEntry            = regexp.MustCompile(`(?m)^[0-9]{10}[ \t]+[0-9]{5}[ \t]+[fn][ \t]*\r?$`)
)

func validatePDFStructure(data []byte) bool {
	if len(data) < len("%PDF-1.0") ||
		(!bytes.HasPrefix(data, []byte("%PDF-1.")) && !bytes.HasPrefix(data, []byte("%PDF-2."))) {
		return false
	}
	version := data[len("%PDF-1.")]
	if version < '0' || version > '9' {
		return false
	}

	trimmed := bytes.TrimRightFunc(data, unicode.IsSpace)
	if !bytes.HasSuffix(trimmed, []byte("%%EOF")) {
		return false
	}
	beforeEOF := trimmed[:len(trimmed)-len("%%EOF")]
	marker := bytes.LastIndex(beforeEOF, []byte("startxref"))
	if marker < 0 {
		return false
	}
	value := strings.TrimSpace(string(beforeEOF[marker+len("startxref"):]))
	fields := strings.Fields(value)
	if len(fields) != 1 {
		return false
	}
	offset, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || offset < 0 || offset >= int64(marker) {
		return false
	}

	xrefSection := bytes.TrimLeftFunc(trimmed[int(offset):marker], unicode.IsSpace)
	var rootSource []byte
	if bytes.HasPrefix(xrefSection, []byte("xref")) {
		trailer := bytes.LastIndex(xrefSection, []byte("trailer"))
		if trailer < len("xref") ||
			!pdfXRefSubsection.Match(xrefSection[len("xref"):trailer]) ||
			!pdfXRefEntry.Match(xrefSection[len("xref"):trailer]) {
			return false
		}
		rootSource = xrefSection[trailer+len("trailer"):]
	} else {
		header := pdfXRefStreamPattern.FindSubmatch(xrefSection)
		endObject := bytes.Index(xrefSection, []byte("endobj"))
		if len(header) != 3 || endObject < 0 || !pdfXRefTypePattern.Match(xrefSection[:endObject]) {
			return false
		}
		rootSource = xrefSection[:endObject]
	}

	root := pdfRootReferencePattern.FindSubmatch(rootSource)
	if len(root) != 3 {
		return false
	}
	objectHeader := append(append(append([]byte(nil), root[1]...), ' '), root[2]...)
	objectHeader = append(objectHeader, []byte(" obj")...)
	rootOffset := bytes.LastIndex(trimmed[:int(offset)], objectHeader)
	if rootOffset < 0 {
		return false
	}
	objectEndRelative := bytes.Index(trimmed[rootOffset+len(objectHeader):int(offset)], []byte("endobj"))
	if objectEndRelative < 0 {
		return false
	}
	objectBody := trimmed[rootOffset+len(objectHeader) : rootOffset+len(objectHeader)+objectEndRelative]
	return pdfCatalogPattern.Match(objectBody)
}
