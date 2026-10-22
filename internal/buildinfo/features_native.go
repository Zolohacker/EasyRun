//go:build cgo && ((windows && amd64) || (linux && (amd64 || arm64)))

package buildinfo

const nativeFace = true
