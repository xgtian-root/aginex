package storage

import (
	"errors"
	"strings"
	"testing"
)

func TestUploadedPartsMatchPreservesOpaqueETagsAndExactInventory(t *testing.T) {
	t.Parallel()

	acknowledged := []UploadedPart{
		{PartNumber: 1, Size: 32 << 20, ETag: `"opaque-one"`},
		{PartNumber: 2, Size: 7, ETag: `"opaque-two"`},
	}
	provider := []UploadedPart{acknowledged[1], acknowledged[0]}
	if !uploadedPartsMatch(acknowledged, provider) {
		t.Fatal("exact provider inventory did not match persisted acknowledgements")
	}
	withoutQuotes := append([]UploadedPart(nil), provider...)
	withoutQuotes[1].ETag = "opaque-one"
	if !uploadedPartsMatch(acknowledged, withoutQuotes) {
		t.Fatal("equivalent provider ETag quoting was not normalized")
	}
	withExtra := append(append([]UploadedPart(nil), provider...), UploadedPart{
		PartNumber: 3, Size: 1, ETag: `"unacknowledged"`,
	})
	if uploadedPartsMatch(acknowledged, withExtra) {
		t.Fatal("unacknowledged provider part was accepted")
	}
}

func TestFormatContentDispositionAlwaysIncludesFilenameStar(t *testing.T) {
	t.Parallel()

	value, err := FormatContentDisposition(ReadDispositionAttachment, "季度 report.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(value, "attachment;") || !strings.Contains(value, "filename=") ||
		!strings.Contains(value, "filename*=UTF-8''%E5%AD%A3%E5%BA%A6%20report.pdf") {
		t.Fatalf("content disposition = %q", value)
	}
	if _, err := FormatContentDisposition(ReadDispositionAttachment, `..\secret.txt`); !errors.Is(err, ErrInvalidFilename) {
		t.Fatalf("backslash filename error = %v", err)
	}
}
