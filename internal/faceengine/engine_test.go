package faceengine

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDecodeBGR(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{R: 40, G: 50, B: 60, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeBGR(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Width != 2 || decoded.Height != 1 {
		t.Fatalf("unexpected dimensions %dx%d", decoded.Width, decoded.Height)
	}
	want := []byte{30, 20, 10, 60, 50, 40}
	if !bytes.Equal(decoded.Pixels, want) {
		t.Fatalf("BGR mismatch: got %v want %v", decoded.Pixels, want)
	}
}
