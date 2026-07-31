package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func validPNG(t *testing.T) []byte {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	source.Set(0, 0, color.NRGBA{R: 64, G: 128, B: 192, A: 255})
	source.Set(1, 1, color.NRGBA{R: 192, G: 128, B: 64, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
