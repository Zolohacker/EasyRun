//go:build !windows || !amd64 || !cgo

package faceengine

import "errors"

func New(Options) (Analyzer, error) {
	return nil, errors.New("local MNN face engine requires Windows amd64 with CGO enabled")
}
