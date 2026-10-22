//go:build !cgo || (!windows && !linux) || (windows && !amd64) || (linux && !amd64 && !arm64)

package faceengine

import "errors"

func New(Options) (Analyzer, error) {
	return nil, errors.New("local MNN face engine requires CGO and Windows amd64 or Linux amd64/arm64")
}
