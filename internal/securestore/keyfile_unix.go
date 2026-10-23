//go:build !windows

package securestore

import "os"

func protectSecretFile(path string) error { return os.Chmod(path, 0600) }
