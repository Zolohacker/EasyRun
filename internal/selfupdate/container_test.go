package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerRuntimeIsSeparateFromData(t *testing.T) {
	// ContainerSupervise passes a canonical root. Windows CI's temp directory
	// can use an 8.3 alias (RUNNER~1), so match that production precondition.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{root, filepath.Dir(root), filepath.Join(root, "data")} {
		if separateContainerData(root, data) == nil {
			t.Fatalf("overlapping program/data directories accepted: %s", data)
		}
	}
	if err := separateContainerData(root, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	missing := filepath.Join(root, "must-not-create")
	if err := ContainerSupervise(ctx, "missing-image", missing, "http://127.0.0.1:8080/healthz"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not respected: %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("cancelled startup created a program directory")
	}
}

func TestContainerBootstrapPreservesOnlineUpdateAcrossRestart(t *testing.T) {
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.WriteFile(image, []byte("image-v1"), 0755); err != nil {
		t.Fatal(err)
	}
	root, err := containerRuntimeRoot(filepath.Join(base, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := prepareContainerRuntime(image, root)
	if err != nil {
		t.Fatal(err)
	}
	if contents, _ := os.ReadFile(target); string(contents) != "image-v1" {
		t.Fatal("initial image was not installed")
	}
	if err := os.WriteFile(target, []byte("online-v2"), 0755); err != nil {
		t.Fatal(err)
	}
	updateDir := filepath.Join(root, ".easyrun-update")
	if err := os.Mkdir(updateDir, 0700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(updateDir, "state.json")
	if err := os.WriteFile(statePath, []byte("preserve pending recovery"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareContainerRuntime(image, root); err != nil {
		t.Fatal(err)
	}
	if contents, _ := os.ReadFile(target); string(contents) != "online-v2" {
		t.Fatal("restart replaced the online-installed program")
	}
	if contents, _ := os.ReadFile(statePath); string(contents) != "preserve pending recovery" {
		t.Fatal("restart discarded supervisor recovery state")
	}
	// An explicit different-image deployment takes precedence over the
	// persisted program, while retaining old update files outside the active path.
	if err := os.WriteFile(image, []byte("image-v3"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareContainerRuntime(image, root); err != nil {
		t.Fatal(err)
	}
	if contents, _ := os.ReadFile(target); string(contents) != "image-v3" {
		t.Fatal("new image did not replace the persisted application")
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("old image's pending update remains active")
	}
	archives, err := filepath.Glob(filepath.Join(root, ".easyrun-update-archived-*", "state.json"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("old update state not retained: %v %v", archives, err)
	}
	if contents, _ := os.ReadFile(archives[0]); string(contents) != "preserve pending recovery" {
		t.Fatal("archived state changed")
	}
	t.Setenv("EASYRUN_CONTAINER_MANAGED", "1")
	t.Setenv("EASYRUN_CONTAINER_RUNTIME", root)
	if !managedContainerRuntime(target) || managedContainerRuntime(image) {
		t.Fatal("managed runtime recognition is not restricted to the volume's program")
	}
	if err := os.WriteFile(filepath.Join(root, containerImageFile), []byte(`{"schema":1,"image_sha":"invalid"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if managedContainerRuntime(target) {
		t.Fatal("invalid volume marker enabled installation")
	}
}

func TestContainerBootstrapHandlesInterruptedInitialization(t *testing.T) {
	for _, missingTarget := range []bool{false, true} {
		t.Run(map[bool]string{false: "manifest missing after copy", true: "target missing after bootstrap"}[missingTarget], func(t *testing.T) {
			base := t.TempDir()
			image := filepath.Join(base, "image")
			if err := os.WriteFile(image, []byte("image-v1"), 0755); err != nil {
				t.Fatal(err)
			}
			root, err := containerRuntimeRoot(filepath.Join(base, "runtime"))
			if err != nil {
				t.Fatal(err)
			}
			target, err := prepareContainerRuntime(image, root)
			if err != nil {
				t.Fatal(err)
			}
			remove := filepath.Join(root, containerImageFile)
			if missingTarget {
				remove = target
			}
			if err := os.Remove(remove); err != nil {
				t.Fatal(err)
			}
			if _, err := prepareContainerRuntime(image, root); err != nil {
				t.Fatal(err)
			}
			if contents, _ := os.ReadFile(target); string(contents) != "image-v1" {
				t.Fatal("interrupted bootstrap did not recover")
			}
			if _, err := readContainerImageState(filepath.Join(root, containerImageFile)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContainerBootstrapRejectsUnknownFilesAndUnsafePaths(t *testing.T) {
	base := t.TempDir()
	image := filepath.Join(base, "image")
	if err := os.WriteFile(image, []byte("image"), 0755); err != nil {
		t.Fatal(err)
	}
	root, err := containerRuntimeRoot(filepath.Join(base, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, binaryName())
	if err := os.WriteFile(target, []byte("unknown-application"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareContainerRuntime(image, root); err == nil {
		t.Fatal("unknown existing application was overwritten")
	}
	if contents, _ := os.ReadFile(target); string(contents) != "unknown-application" {
		t.Fatal("existing data changed")
	}
	if _, err := prepareContainerRuntime(target, root); err == nil {
		t.Fatal("image and replaceable program must differ")
	}
	for _, bad := range []string{"", "runtime", filepath.VolumeName(base) + string(filepath.Separator)} {
		if _, err := containerRuntimeRoot(bad); err == nil {
			t.Fatalf("unsafe runtime root accepted: %q", bad)
		}
	}
	if err := os.WriteFile(filepath.Join(root, containerImageFile), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareContainerRuntime(image, root); err == nil || !strings.Contains(err.Error(), "镜像标记") {
		t.Fatalf("corrupt marker not diagnosed: %v", err)
	}
}

func TestContainerRuntimeLockIsExclusiveAndReleasedOnClose(t *testing.T) {
	root := t.TempDir()
	first, err := acquireRuntimeLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := acquireRuntimeLock(root); err == nil {
		second.Close()
		t.Fatal("two supervisors acquired the same program volume")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := acquireRuntimeLock(root)
	if err != nil {
		t.Fatalf("runtime lock not released: %v", err)
	}
	third.Close()
}
