//go:build windows

package selfupdate

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureChild(cmd *exec.Cmd)             { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
func requestChildShutdown(p *os.Process) error { return p.Kill() }
func lockRuntimeFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func replaceStateFile(from, to string) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	for i := 0; i < 40; i++ {
		err = windows.MoveFileEx(f, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}
func waitWritable(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var err error
	for time.Now().Before(deadline) {
		var f *os.File
		f, err = os.OpenFile(name, os.O_RDWR, 0)
		if err == nil {
			_ = f.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}
