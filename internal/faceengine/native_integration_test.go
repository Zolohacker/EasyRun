//go:build cgo && ((windows && amd64) || (linux && (amd64 || arm64)))

package faceengine

import (
	"context"
	"os"
	"testing"
)

func TestNativeEngineIntegration(t *testing.T) {
	library := os.Getenv("FACE_ENGINE_LIBRARY_PATH")
	pack := os.Getenv("FACE_MODEL_PACK_PATH")
	imagePath := os.Getenv("FACE_TEST_IMAGE")
	if library == "" || pack == "" || imagePath == "" {
		t.Skip("set FACE_ENGINE_LIBRARY_PATH, FACE_MODEL_PACK_PATH, and FACE_TEST_IMAGE")
	}
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{LibraryPath: library, ResourcePack: pack, DetectPixelSize: 320})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	comparison, err := engine.Compare(context.Background(), imageData, imageData)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Cosine < 0.99 || comparison.Confidence < 99 {
		t.Fatalf("same-image comparison too low: %+v", comparison)
	}
}
