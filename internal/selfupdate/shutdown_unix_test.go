//go:build !windows

package selfupdate

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestGracefulChildHelper(t *testing.T) {
	dir := os.Getenv("EASYRUN_TEST_SHUTDOWN_DIR")
	if dir == "" {
		return
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM)
	defer signal.Stop(stop)
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	<-stop
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "closed"), []byte("closed"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownChildForwardsSIGTERM(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGracefulChildHelper$")
	cmd.Env = append(os.Environ(), "EASYRUN_TEST_SHUTDOWN_DIR="+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	defer stopChild(cmd, done)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	shutdownChild(cmd, done)
	if _, err := os.Stat(filepath.Join(dir, "closed")); err != nil {
		t.Fatalf("child was killed before graceful shutdown completed: %v", err)
	}
	if !cmd.ProcessState.Success() {
		t.Fatalf("child did not exit cleanly: %v", cmd.ProcessState)
	}
}
