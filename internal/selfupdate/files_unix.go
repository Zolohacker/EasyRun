//go:build !windows

package selfupdate

import (
	"os"
	"os/exec"
	"time"
)

func configureChild(*exec.Cmd)                 {}
func replaceStateFile(from, to string) error   { return os.Rename(from, to) }
func waitWritable(string, time.Duration) error { return nil }
