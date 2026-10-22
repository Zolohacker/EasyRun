package faceengine

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	_ "image/png"
	"path/filepath"
)

// Analyzer compares a reference portrait with a single accepted camera frame.
// It intentionally exposes only the recognition result; capture policy,
// interaction actions and liveness are separate concerns of the caller.
type Analyzer interface {
	Compare(context.Context, []byte, []byte) (Comparison, error)
	Name() string
	Close() error
}

type Comparison struct {
	Cosine                       float64
	Confidence                   float64
	ReferenceDetectionConfidence float64
	ProbeDetectionConfidence     float64
}

type Options struct {
	LibraryPath     string
	ResourcePack    string
	DetectPixelSize int
}

func (o Options) validate() (Options, error) {
	if o.LibraryPath == "" {
		return Options{}, errors.New("FACE_ENGINE_LIBRARY_PATH is empty")
	}
	if o.ResourcePack == "" {
		return Options{}, errors.New("FACE_MODEL_PACK_PATH is empty")
	}
	var err error
	o.LibraryPath, err = filepath.Abs(o.LibraryPath)
	if err != nil {
		return Options{}, fmt.Errorf("resolve face engine library: %w", err)
	}
	o.ResourcePack, err = filepath.Abs(o.ResourcePack)
	if err != nil {
		return Options{}, fmt.Errorf("resolve face model pack: %w", err)
	}
	if o.DetectPixelSize == 0 {
		o.DetectPixelSize = 320
	}
	switch o.DetectPixelSize {
	case 160, 320, 640:
	default:
		return Options{}, errors.New("FACE_DETECT_PIXEL_SIZE must be 160, 320, or 640")
	}
	return o, nil
}

type bgrImage struct {
	Pixels        []byte
	Width, Height int
}

func decodeBGR(encoded []byte) (bgrImage, error) {
	if len(encoded) == 0 {
		return bgrImage{}, errors.New("face image is empty")
	}
	img, _, err := image.Decode(newByteReader(encoded))
	if err != nil {
		return bgrImage{}, fmt.Errorf("decode face image: %w", err)
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width < 1 || height < 1 || width > 8192 || height > 8192 || int64(width)*int64(height) > 32_000_000 {
		return bgrImage{}, fmt.Errorf("unsupported face image dimensions %dx%d", width, height)
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(nrgba, nrgba.Bounds(), img, bounds.Min, draw.Src)
	pixels := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source := nrgba.PixOffset(x, y)
			target := (y*width + x) * 3
			pixels[target] = nrgba.Pix[source+2]
			pixels[target+1] = nrgba.Pix[source+1]
			pixels[target+2] = nrgba.Pix[source]
		}
	}
	return bgrImage{Pixels: pixels, Width: width, Height: height}, nil
}
