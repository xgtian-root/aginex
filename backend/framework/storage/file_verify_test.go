package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"strings"
	"testing"
)

func TestFileVerifierDerivesArbitraryFileMetadataAndSafePreviews(t *testing.T) {
	t.Parallel()

	pngContent := encodePNG(t, 3, 2)
	jpegContent := encodeJPEG(t, 4, 3)
	webPContent := decodeBase64(t, tinyWebPBase64)
	gifContent := encodeGIF(t, 5, 4)
	pdfContent := validTestPDF()
	textContent := []byte("plain text is a valid arbitrary file\n")

	tests := []struct {
		name       string
		content    []byte
		mimeType   string
		preview    PreviewKind
		wantWidth  int
		wantHeight int
	}{
		{name: "png", content: pngContent, mimeType: MIMEPNG, preview: PreviewImage, wantWidth: 3, wantHeight: 2},
		{name: "jpeg", content: jpegContent, mimeType: MIMEJPEG, preview: PreviewImage, wantWidth: 4, wantHeight: 3},
		{name: "webp", content: webPContent, mimeType: MIMEWebP, preview: PreviewImage, wantWidth: 75, wantHeight: 100},
		{name: "gif", content: gifContent, mimeType: MIMEGIF, preview: PreviewImage, wantWidth: 5, wantHeight: 4},
		{name: "pdf", content: pdfContent, mimeType: MIMEPDF, preview: PreviewPDF},
		{name: "arbitrary text", content: textContent, mimeType: "text/plain; charset=utf-8", preview: PreviewNone},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			verified, err := newDefaultFileVerifier(t).Verify(
				context.Background(), bytes.NewReader(test.content), int64(len(test.content)),
			)
			if err != nil {
				t.Fatal(err)
			}
			wantDigest := fmt.Sprintf("%x", sha256.Sum256(test.content))
			if verified.MIMEType != test.mimeType ||
				verified.Size != int64(len(test.content)) ||
				verified.SHA256 != wantDigest ||
				verified.PreviewKind != test.preview ||
				verified.Width != test.wantWidth ||
				verified.Height != test.wantHeight {
				t.Fatalf("verified = %#v", verified)
			}
		})
	}
}

func TestFileVerifierDowngradesMalformedPreviewCandidates(t *testing.T) {
	t.Parallel()

	pngContent := encodePNG(t, 2, 2)
	gifContent := encodeGIF(t, 2, 2)
	jpegContent := encodeJPEG(t, 3, 2)
	corruptedJPEG := append(append([]byte(nil), jpegContent[:len(jpegContent)/2]...), 0xff, 0xd9)
	tests := []struct {
		name    string
		content []byte
	}{
		{name: "truncated png", content: pngContent[:len(pngContent)-8]},
		{name: "corrupted jpeg scan data", content: corruptedJPEG},
		{name: "png trailing script", content: append(append([]byte(nil), pngContent...), []byte("<script>x</script>")...)},
		{name: "truncated gif", content: gifContent[:len(gifContent)-1]},
		{name: "pdf missing eof", content: []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\nstartxref\n9\n")},
		{name: "pdf impossible xref", content: []byte("%PDF-1.7\nstartxref\n999999\n%%EOF\n")},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			verified, err := newDefaultFileVerifier(t).Verify(
				context.Background(), bytes.NewReader(test.content), int64(len(test.content)),
			)
			if err != nil {
				t.Fatalf("malformed preview candidate rejected the arbitrary file: %v", err)
			}
			if verified.MIMEType != StoredContentType || verified.PreviewKind != PreviewNone ||
				verified.Width != 0 || verified.Height != 0 || verified.SHA256 == "" {
				t.Fatalf("verified = %#v", verified)
			}
		})
	}
}

func TestFileVerifierRejectsOnlyStreamAndPolicyFailures(t *testing.T) {
	t.Parallel()

	verifier := newDefaultFileVerifier(t)
	if _, err := verifier.Verify(context.Background(), strings.NewReader(""), 1); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("empty error = %v", err)
	}
	if _, err := verifier.Verify(context.Background(), strings.NewReader("abc"), 4); !errors.Is(err, ErrFileSizeMismatch) {
		t.Fatalf("size mismatch error = %v", err)
	}
	if _, err := verifier.Verify(context.Background(), strings.NewReader("abc"), 0); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("zero intent error = %v", err)
	}

	policy, err := NewFilePolicy(2)
	if err != nil {
		t.Fatal(err)
	}
	limited, err := NewFileVerifier(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limited.Verify(context.Background(), strings.NewReader("abc"), 3); !errors.Is(err, ErrContentTooLarge) {
		t.Fatalf("limit error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := verifier.Verify(ctx, strings.NewReader("abc"), 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestNewFilePolicyEnforcesAbsoluteLimit(t *testing.T) {
	t.Parallel()

	if _, err := NewFilePolicy(0); !errors.Is(err, ErrInvalidFilePolicy) {
		t.Fatalf("zero policy error = %v", err)
	}
	if _, err := NewFilePolicy(AbsoluteMaxFileBytes + 1); !errors.Is(err, ErrInvalidFilePolicy) {
		t.Fatalf("oversized policy error = %v", err)
	}
	policy, err := NewFilePolicy(AbsoluteMaxFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	if policy.MaxBytes != AbsoluteMaxFileBytes || policy.MaxPreviewBytes != defaultMaxPreviewBytes {
		t.Fatalf("policy = %#v", policy)
	}
}

func newDefaultFileVerifier(t *testing.T) *FileVerifier {
	t.Helper()
	verifier, err := NewFileVerifier(DefaultFilePolicy())
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func encodeGIF(t *testing.T, width, height int) []byte {
	t.Helper()
	palette := color.Palette{color.Black, color.White}
	imageData := image.NewPaletted(image.Rect(0, 0, width, height), palette)
	imageData.SetColorIndex(0, 0, 1)
	var output bytes.Buffer
	if err := gif.Encode(&output, imageData, nil); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func validTestPDF() []byte {
	prefix := "%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"
	return []byte(fmt.Sprintf(
		"%sxref\n0 2\n0000000000 65535 f \n0000000009 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		prefix,
		len(prefix),
	))
}
