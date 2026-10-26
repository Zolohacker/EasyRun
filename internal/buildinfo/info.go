package buildinfo

import (
	"github.com/Zolohacker/EasyRun/internal/protocol"
	"runtime"
)

var Version = "0.1.8"
var Commit = "source"
var BuiltAt = "unknown"
var BuildType = "source"

type Info struct {
	Version          string `json:"version"`
	Commit           string `json:"commit"`
	BuiltAt          string `json:"built_at"`
	BuildType        string `json:"build_type"`
	OS               string `json:"os"`
	Arch             string `json:"arch"`
	NativeFace       bool   `json:"native_face"`
	ContainerRuntime int    `json:"container_runtime"`
	ClientVersion    string `json:"client_version"`
	ClientBuild      string `json:"client_build"`
}

func Current() Info {
	return Info{Version: Version, Commit: Commit, BuiltAt: BuiltAt, BuildType: BuildType, OS: runtime.GOOS, Arch: runtime.GOARCH, NativeFace: nativeFace, ContainerRuntime: 1, ClientVersion: protocol.AppVersion, ClientBuild: protocol.BuildVersion}
}
