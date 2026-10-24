package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const containerImageFile = "image.json"

type containerImageState struct {
	Schema   int    `json:"schema"`
	ImageSHA string `json:"image_sha"`
}

// ContainerSupervise runs from the immutable image binary, keeping only the
// replaceable application and update state in a dedicated persistent volume.
// It never needs the Docker socket, root privileges or the application data dir.
func ContainerSupervise(ctx context.Context, image, runtimeDir, address string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := containerRuntimeRoot(runtimeDir)
	if err != nil {
		return err
	}
	if err := separateContainerData(root, os.Getenv("EASYRUN_DATA_DIR")); err != nil {
		return err
	}
	lock, err := acquireRuntimeLock(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	target, err := prepareContainerRuntime(image, root)
	if err != nil {
		return err
	}
	return supervise(ctx, target, address, []string{
		"EASYRUN_CONTAINER_MANAGED=1",
		"EASYRUN_CONTAINER_RUNTIME=" + root,
	})
}

func separateContainerData(root, data string) error {
	if data == "" {
		data = "data"
	}
	abs, err := filepath.Abs(data)
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	inside := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	if inside(root, abs) || inside(abs, root) {
		return errors.New("容器程序卷与数据目录应相互独立，请分别挂载 /app/runtime 和 /app/data")
	}
	return nil
}

func containerRuntimeRoot(dir string) (string, error) {
	if !filepath.IsAbs(dir) || filepath.Dir(filepath.Clean(dir)) == filepath.Clean(dir) {
		return "", errors.New("容器程序卷必须是独立的绝对目录，建议 /app/runtime")
	}
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", fmt.Errorf("准备容器程序卷 %s: %w", dir, err)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if filepath.Dir(root) == root {
		return "", errors.New("容器程序卷指向文件系统根目录")
	}
	if err := probeWritableDirectory(root); err != nil {
		return "", fmt.Errorf("容器程序卷 %s 需要服务账号的写入权限: %w", root, err)
	}
	return root, nil
}

func acquireRuntimeLock(root string) (*os.File, error) {
	name := filepath.Join(root, ".runtime.lock")
	if err := regularFileOrAbsent(name); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("打开容器程序卷锁: %w", err)
	}
	if err := lockRuntimeFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("容器程序卷正在被其他实例使用或不支持文件锁，请为每个实例配置独立程序卷: %w", err)
	}
	return f, nil
}

func regularFileOrAbsent(name string) error {
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("容器程序路径应为普通文件: %s", name)
	}
	return nil
}

// The image hash is the bootstrap generation. Restarting the same image keeps
// an online-installed application; explicitly deploying a different image
// switches back to that image's binary, including deliberate image downgrades.
// Caller must hold the runtime lock for this function and the child's lifetime.
func prepareContainerRuntime(image, root string) (string, error) {
	image, err := filepath.EvalSymlinks(image)
	if err != nil {
		return "", err
	}
	image, err = filepath.Abs(image)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, binaryName())
	if strings.EqualFold(image, target) {
		return "", errors.New("容器守护程序应从镜像启动，与程序卷中的应用保持分离")
	}
	manifest := filepath.Join(root, containerImageFile)
	for _, name := range []string{target, manifest} {
		if err := regularFileOrAbsent(name); err != nil {
			return "", err
		}
	}
	imageSHA, err := fileHash(image)
	if err != nil {
		return "", err
	}
	state, err := readContainerImageState(manifest)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("读取程序卷镜像标记: %w", err)
	}
	targetSHA, targetErr := fileHash(target)
	if targetErr != nil && !errors.Is(targetErr, os.ErrNotExist) {
		return "", targetErr
	}
	if err == nil && state.ImageSHA == imageSHA && targetErr == nil {
		return target, nil
	}
	if errors.Is(err, os.ErrNotExist) && targetErr == nil && targetSHA != imageSHA {
		return "", errors.New("程序卷含有缺少镜像标记的应用，请核对后使用独立空程序卷；现有文件已保留")
	}
	// Preserve old update files for inspection, but never apply a prepared
	// update or offer a rollback belonging to another image generation.
	updateDir := filepath.Join(root, ".easyrun-update")
	if info, err := os.Lstat(updateDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("更新暂存路径应为普通目录: %s", updateDir)
		}
		archive := filepath.Join(root, fmt.Sprintf(".easyrun-update-archived-%d", time.Now().UnixNano()))
		if err := os.Rename(updateDir, archive); err != nil {
			return "", fmt.Errorf("保留上一镜像的更新状态: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if targetSHA != imageSHA {
		if err := seedContainerBinary(image, target, imageSHA); err != nil {
			return "", err
		}
	}
	if err := writeJSONAtomic(manifest, containerImageState{Schema: 1, ImageSHA: imageSHA}); err != nil {
		return "", err
	}
	return target, nil
}

func readContainerImageState(name string) (containerImageState, error) {
	var state containerImageState
	raw, err := os.ReadFile(name)
	if err != nil {
		return state, err
	}
	if len(raw) > 4096 {
		return state, errors.New("程序卷镜像标记过大")
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	if state.Schema != 1 || len(state.ImageSHA) != 64 || strings.Trim(state.ImageSHA, "0123456789abcdef") != "" {
		return state, errors.New("程序卷镜像标记格式不正确")
	}
	return state, nil
}

func seedContainerBinary(image, target, expectedSHA string) error {
	f, err := os.CreateTemp(filepath.Dir(target), ".container-seed-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Close(); err != nil {
		return err
	}
	if err := copyFile(image, name, 0755); err != nil {
		return err
	}
	if hash, err := fileHash(name); err != nil || hash != expectedSHA {
		return errors.New("镜像程序复制校验失败，保留原程序")
	}
	if err := os.Chmod(name, 0755); err != nil {
		return err
	}
	return replaceStateFile(name, target)
}

func managedContainerRuntime(exe string) bool {
	if os.Getenv("EASYRUN_CONTAINER_MANAGED") != "1" {
		return false
	}
	root := os.Getenv("EASYRUN_CONTAINER_RUNTIME")
	if !filepath.IsAbs(root) || filepath.Dir(filepath.Clean(root)) == filepath.Clean(root) {
		return false
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil || exe != filepath.Join(root, binaryName()) {
		return false
	}
	manifest := filepath.Join(root, containerImageFile)
	if regularFileOrAbsent(manifest) != nil {
		return false
	}
	_, err = readContainerImageState(manifest)
	return err == nil
}
