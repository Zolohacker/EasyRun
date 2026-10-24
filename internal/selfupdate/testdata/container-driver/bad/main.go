// Isolated container update failure fixture; never shipped with the application.
package main

import (
	"encoding/json"
	"os"
	"runtime"
	"strconv"
)

var version, protocol = "0.1.7", "1"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version-json" {
		p, _ := strconv.Atoi(protocol)
		json.NewEncoder(os.Stdout).Encode(map[string]any{
			"version": version, "commit": "container-failure-fixture", "built_at": "fixture", "build_type": "release",
			"os": runtime.GOOS, "arch": runtime.GOARCH, "native_face": true, "container_runtime": p,
		})
		return
	}
	os.Exit(3)
}
