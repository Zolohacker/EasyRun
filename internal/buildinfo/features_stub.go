//go:build !cgo || (!windows && !linux) || (windows && !amd64) || (linux && !amd64 && !arm64)

package buildinfo

const nativeFace = false
