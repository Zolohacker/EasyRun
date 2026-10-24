package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDeploymentCapabilityPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		supervised, container, managed bool
		want                           string
	}{
		{"container without supervisor", false, true, false, "容器部署"},
		{"container with supervisor", true, true, false, "容器部署"},
		{"managed container", true, true, true, ""},
		{"managed container without supervisor", false, true, true, "守护启动模式"},
		{"standalone", false, false, false, "守护启动模式"},
		{"supervised binary", true, false, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := deploymentCapability(tc.supervised, tc.container, tc.managed)
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("reason=%q want=%q", got, tc.want)
			}
			if tc.want != "" {
				// A deployment restriction must not be overwritten by, or even
				// attempt, filesystem access under an unwritable/invalid path.
				root := t.TempDir()
				blocked := filepath.Join(root, "not-a-directory")
				if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				s := &Service{exe: filepath.Join(blocked, binaryName()), dir: filepath.Join(blocked, ".easyrun-update"), capability: got}
				if reason := s.installCapability(); reason != got {
					t.Fatalf("deployment reason overwritten: %q", reason)
				}
				data, err := os.ReadFile(blocked)
				if err != nil || string(data) != "keep" {
					t.Fatal("deployment check mutated the filesystem")
				}
			}
		})
	}
}

func newCapabilityTestService(t *testing.T) *Service {
	t.Helper()
	root := t.TempDir()
	exe := filepath.Join(root, binaryName())
	if err := os.WriteFile(exe, []byte("keep executable unchanged"), 0755); err != nil {
		t.Fatal(err)
	}
	return &Service{exe: exe, dir: filepath.Join(root, ".easyrun-update"), statePath: filepath.Join(root, ".easyrun-update", "state.json"), status: Status{HasUpdate: true}}
}

func TestCapabilityRecoversWithoutRestart(t *testing.T) {
	s := newCapabilityTestService(t)
	if err := os.WriteFile(s.dir, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked := s.Snapshot(context.Background())
	if blocked.CanInstall || !strings.Contains(blocked.CapabilityReason, s.dir) || !strings.Contains(blocked.CapabilityReason, "准备更新目录") {
		t.Fatalf("incorrect path diagnostic: %+v", blocked)
	}
	if err := s.Start(context.Background(), false); err == nil || !strings.Contains(err.Error(), "准备更新目录") {
		t.Fatalf("installation did not recheck the path: %v", err)
	}
	if err := os.Remove(s.dir); err != nil {
		t.Fatal(err)
	}
	recovered := s.Snapshot(context.Background())
	if !recovered.CanInstall || recovered.CapabilityReason != "" {
		t.Fatalf("capability remained blocked after repair: %+v", recovered)
	}
	if err := s.Start(context.Background(), false); err == nil || !strings.Contains(err.Error(), "守护组件尚未配置") {
		t.Fatalf("installation kept a stale permission error: %v", err)
	}
	if data, err := os.ReadFile(s.exe); err != nil || string(data) != "keep executable unchanged" {
		t.Fatal("permission check changed the executable")
	}
	for _, dir := range []string{filepath.Dir(s.exe), s.dir} {
		matches, err := filepath.Glob(filepath.Join(dir, ".easyrun-write-check-*"))
		if err != nil || len(matches) != 0 {
			t.Fatalf("write probe left temporary files: %v %v", matches, err)
		}
	}
}

func TestCapabilityChecksExecutableDirectoryNotJustStaging(t *testing.T) {
	s := newCapabilityTestService(t)
	blocked := filepath.Join(filepath.Dir(s.exe), "blocked-parent")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s.exe = filepath.Join(blocked, binaryName())
	if reason := s.installCapability(); !strings.Contains(reason, "程序目录") || !strings.Contains(reason, blocked) {
		t.Fatalf("missing executable directory diagnostic: %q", reason)
	}
}

func TestCapabilityDetectsRealReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows directory permissions are ACL-based, not POSIX mode bits")
	}
	s := newCapabilityTestService(t)
	if reason := s.installCapability(); reason != "" {
		t.Fatal(reason)
	}
	parent := filepath.Dir(s.exe)
	if err := os.Chmod(parent, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0755) })
	if err := probeWritableDirectory(parent); err == nil {
		t.Skip("test process can write despite mode bits (for example, root)")
	}
	if reason := s.installCapability(); !strings.Contains(reason, "程序目录") {
		t.Fatalf("read-only executable directory accepted: %q", reason)
	}
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.dir, 0700) })
	if reason := s.installCapability(); !strings.Contains(reason, "更新目录") {
		t.Fatalf("read-only staging directory accepted: %q", reason)
	}
	if err := os.Chmod(s.dir, 0700); err != nil {
		t.Fatal(err)
	}
	if reason := s.installCapability(); reason != "" {
		t.Fatalf("capability remained blocked after chmod: %q", reason)
	}
}
