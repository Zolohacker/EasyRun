//go:build cgo && ((windows && amd64) || (linux && (amd64 || arm64)))

package faceengine

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBundledModelAndNativeRuntimeInitialize(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(source), "..", "..", "models")
	name := "libInspireFace.so"
	if runtime.GOOS == "windows" {
		name = "libInspireFace.dll"
	}
	relative := runtime.GOOS + "-" + runtime.GOARCH + "/" + name
	manifest, err := os.ReadFile(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"Megatron", relative} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(raw)
		if !strings.Contains(string(manifest), hex.EncodeToString(h[:])+"  "+f) {
			t.Fatalf("bundled checksum differs: %s", f)
		}
	}
	engine, err := New(Options{LibraryPath: filepath.Join(root, filepath.FromSlash(relative)), ResourcePack: filepath.Join(root, "Megatron"), DetectPixelSize: 320})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
}
