package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestImageVerifierAcceptsDecodedImagesAndDerivesMetadata(t *testing.T) {
	t.Parallel()

	pngContent := encodePNG(t, 3, 2)
	jpegContent := encodeJPEG(t, 4, 3)
	webPContent := decodeBase64(t, tinyWebPBase64)
	verifier := newDefaultVerifier(t)

	tests := []struct {
		name        string
		content     []byte
		claimedMIME string
		wantMIME    string
		wantWidth   int
		wantHeight  int
	}{
		{"png", pngContent, "IMAGE/PNG; charset=binary", MIMEPNG, 3, 2},
		{"jpeg", jpegContent, MIMEJPEG, MIMEJPEG, 4, 3},
		{"webp", webPContent, MIMEWebP, MIMEWebP, 75, 100},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			verified, err := verifier.Verify(
				context.Background(),
				bytes.NewReader(test.content),
				test.claimedMIME,
			)
			if err != nil {
				t.Fatal(err)
			}
			wantDigest := fmt.Sprintf("%x", sha256.Sum256(test.content))
			if verified.MIMEType != test.wantMIME ||
				verified.Size != int64(len(test.content)) ||
				verified.SHA256 != wantDigest ||
				verified.Width != test.wantWidth ||
				verified.Height != test.wantHeight {
				t.Fatalf("verified = %#v", verified)
			}
		})
	}
}

func TestImageVerifierRejectsUnsafeContent(t *testing.T) {
	t.Parallel()

	pngContent := encodePNG(t, 3, 2)
	tests := []struct {
		name    string
		policy  Policy
		content []byte
		claimed string
		wantErr error
	}{
		{
			name:    "over byte limit",
			policy:  policyWith(func(policy *Policy) { policy.MaxBytes = int64(len(pngContent) - 1) }),
			content: pngContent,
			claimed: MIMEPNG,
			wantErr: ErrContentTooLarge,
		},
		{
			name:    "fake claimed MIME",
			policy:  DefaultImagePolicy(),
			content: pngContent,
			claimed: MIMEJPEG,
			wantErr: ErrMIMETypeMismatch,
		},
		{
			name:    "unsupported claim",
			policy:  DefaultImagePolicy(),
			content: pngContent,
			claimed: "image/svg+xml",
			wantErr: ErrUnsupportedMIMEType,
		},
		{
			name:    "non image",
			policy:  DefaultImagePolicy(),
			content: []byte("<html>not an image</html>"),
			claimed: MIMEPNG,
			wantErr: ErrUnsupportedMIMEType,
		},
		{
			name:    "truncated PNG",
			policy:  DefaultImagePolicy(),
			content: pngContent[:len(pngContent)-8],
			claimed: MIMEPNG,
			wantErr: ErrMalformedImage,
		},
		{
			name:    "undecodable PNG body",
			policy:  DefaultImagePolicy(),
			content: corruptPNGImageData(t, pngContent),
			claimed: MIMEPNG,
			wantErr: ErrMalformedImage,
		},
		{
			name:    "PNG with appended payload",
			policy:  DefaultImagePolicy(),
			content: append(append([]byte(nil), pngContent...), []byte("<script>payload</script>")...),
			claimed: MIMEPNG,
			wantErr: ErrMalformedImage,
		},
		{
			name:    "corrupt JPEG",
			policy:  DefaultImagePolicy(),
			content: []byte{0xff, 0xd8, 0xff, 0xda, 0x00, 0x08, 1, 2, 3},
			claimed: MIMEJPEG,
			wantErr: ErrMalformedImage,
		},
		{
			name:    "WebP with inconsistent RIFF length",
			policy:  DefaultImagePolicy(),
			content: append(decodeBase64(t, tinyWebPBase64), 0),
			claimed: MIMEWebP,
			wantErr: ErrMalformedImage,
		},
		{
			name:   "dimension limit before decode",
			policy: policyWith(func(policy *Policy) { policy.MaxWidth = 100 }),
			content: rewritePNGDimensions(
				t,
				pngContent,
				10_000,
				2,
			),
			claimed: MIMEPNG,
			wantErr: ErrImageDimensionsExceeded,
		},
		{
			name: "decoded pixel limit before decode",
			policy: policyWith(func(policy *Policy) {
				policy.MaxWidth = 2000
				policy.MaxHeight = 2000
				policy.MaxPixels = 100
			}),
			content: rewritePNGDimensions(
				t,
				pngContent,
				1000,
				1000,
			),
			claimed: MIMEPNG,
			wantErr: ErrImagePixelsExceeded,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			verifier, err := NewImageVerifier(test.policy)
			if err != nil {
				t.Fatal(err)
			}
			_, err = verifier.Verify(
				context.Background(),
				bytes.NewReader(test.content),
				test.claimed,
			)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestImageVerifierHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newDefaultVerifier(t).Verify(ctx, strings.NewReader("unused"), MIMEPNG)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestNewImageVerifierRejectsUnsafePoliciesAndClonesAllowedTypes(t *testing.T) {
	t.Parallel()

	tests := []Policy{
		{},
		policyWith(func(policy *Policy) { policy.MaxBytes = 0 }),
		policyWith(func(policy *Policy) { policy.MaxBytes = int64(^uint(0) >> 1) }),
		policyWith(func(policy *Policy) { policy.MaxWidth = 0 }),
		policyWith(func(policy *Policy) { policy.MaxHeight = 0 }),
		policyWith(func(policy *Policy) { policy.MaxPixels = 0 }),
		policyWith(func(policy *Policy) { policy.AllowedMIMETypes = nil }),
		policyWith(func(policy *Policy) { policy.AllowedMIMETypes = []string{"image/svg+xml"} }),
	}
	for index, policy := range tests {
		if _, err := NewImageVerifier(policy); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("policy %d: error = %v, want ErrInvalidPolicy", index, err)
		}
	}

	policy := DefaultImagePolicy()
	verifier, err := NewImageVerifier(policy)
	if err != nil {
		t.Fatal(err)
	}
	policy.AllowedMIMETypes[0] = "image/svg+xml"
	if _, err := verifier.Verify(
		context.Background(),
		bytes.NewReader(encodePNG(t, 1, 1)),
		MIMEPNG,
	); err != nil {
		t.Fatalf("mutation of caller policy affected verifier: %v", err)
	}
}

func TestImageVerifierAllowsContentExactlyAtByteLimit(t *testing.T) {
	t.Parallel()

	content := encodePNG(t, 1, 1)
	policy := DefaultImagePolicy()
	policy.MaxBytes = int64(len(content))
	verifier, err := NewImageVerifier(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), bytes.NewReader(content), MIMEPNG); err != nil {
		t.Fatal(err)
	}
}

func TestImageVerifierStopsReadingAfterByteLimit(t *testing.T) {
	t.Parallel()

	policy := DefaultImagePolicy()
	policy.MaxBytes = 32
	verifier, err := NewImageVerifier(policy)
	if err != nil {
		t.Fatal(err)
	}
	reader := &countingReader{remaining: 1 << 20}
	_, err = verifier.Verify(context.Background(), reader, MIMEPNG)
	if !errors.Is(err, ErrContentTooLarge) {
		t.Fatalf("error = %v, want ErrContentTooLarge", err)
	}
	if reader.read != policy.MaxBytes+1 {
		t.Fatalf("bytes read = %d, want %d", reader.read, policy.MaxBytes+1)
	}
}

func newDefaultVerifier(t *testing.T) *ImageVerifier {
	t.Helper()
	verifier, err := NewImageVerifier(DefaultImagePolicy())
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func policyWith(change func(*Policy)) Policy {
	policy := DefaultImagePolicy()
	change(&policy)
	return policy
}

func encodePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	source.Set(0, 0, color.NRGBA{R: 0x28, G: 0x86, B: 0xc4, A: 0xff})
	var output bytes.Buffer
	if err := png.Encode(&output, source); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	source.Set(0, 0, color.NRGBA{R: 0xf0, G: 0x91, B: 0x37, A: 0xff})
	var output bytes.Buffer
	if err := jpeg.Encode(&output, source, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func rewritePNGDimensions(
	t *testing.T,
	content []byte,
	width uint32,
	height uint32,
) []byte {
	t.Helper()
	if len(content) < 33 || !bytes.Equal(content[:8], pngSignature) {
		t.Fatal("test PNG is missing its IHDR")
	}
	rewritten := append([]byte(nil), content...)
	binary.BigEndian.PutUint32(rewritten[16:20], width)
	binary.BigEndian.PutUint32(rewritten[20:24], height)
	binary.BigEndian.PutUint32(rewritten[29:33], crc32.ChecksumIEEE(rewritten[12:29]))
	return rewritten
}

func corruptPNGImageData(t *testing.T, content []byte) []byte {
	t.Helper()
	rewritten := append([]byte(nil), content...)
	for offset := len(pngSignature); offset < len(rewritten); {
		if len(rewritten)-offset < 12 {
			t.Fatal("test PNG contains a truncated chunk")
		}
		length := int(binary.BigEndian.Uint32(rewritten[offset : offset+4]))
		chunkEnd := offset + 12 + length
		if chunkEnd > len(rewritten) {
			t.Fatal("test PNG chunk exceeds content")
		}
		if bytes.Equal(rewritten[offset+4:offset+8], []byte("IDAT")) {
			if length == 0 {
				t.Fatal("test PNG contains an empty IDAT")
			}
			rewritten[offset+8] ^= 0xff
			binary.BigEndian.PutUint32(
				rewritten[chunkEnd-4:chunkEnd],
				crc32.ChecksumIEEE(rewritten[offset+4:chunkEnd-4]),
			)
			return rewritten
		}
		offset = chunkEnd
	}
	t.Fatal("test PNG is missing IDAT")
	return nil
}

func decodeBase64(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

type countingReader struct {
	remaining int64
	read      int64
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, nil
	}
	count := int64(len(buffer))
	if count > reader.remaining {
		count = reader.remaining
	}
	for index := int64(0); index < count; index++ {
		buffer[index] = 'x'
	}
	reader.remaining -= count
	reader.read += count
	return int(count), nil
}

// A small lossless WebP from the Go image package's BSD-licensed test corpus.
const tinyWebPBase64 = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="
