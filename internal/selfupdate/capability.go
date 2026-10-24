package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Zolohacker/EasyRun/internal/buildinfo"
)

func runningInContainer() bool {
	if buildinfo.BuildType == "container" {
		return true
	}
	if runtime.GOOS == "linux" {
		for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
			if _, err := os.Stat(marker); err == nil {
				return true
			}
		}
	}
	return false
}

func deploymentCapability(supervised, container bool) string {
	if container {
		return "当前为容器部署，请在宿主机更新镜像并重建应用容器；此处仍可检查发行版本"
	}
	if !supervised {
		return "当前不是内置守护启动模式，可检查版本；请使用 start-local 或 --supervise 启动以在线安装和回滚"
	}
	return ""
}

// Deployment restrictions are stable, but filesystem permissions can be fixed
// while the service is running. Recheck them when displaying or using update
// capabilities instead of caching a startup failure until the next restart.
func (s *Service) installCapability() string {
	if s.capability != "" {
		return s.capability
	}
	if s.exe == "" || s.dir == "" {
		return "程序或更新目录路径尚未初始化"
	}
	// The staging directory alone is insufficient: replacement and rollback
	// also require creating/removing files beside the executable. Do not open
	// the live EXE for writing, since Windows intentionally locks that image.
	parent := filepath.Dir(s.exe)
	if err := probeWritableDirectory(parent); err != nil {
		return fmt.Sprintf("程序目录 %s 的写入检测失败：%v。请检查服务运行账号的目录权限，修复后重新检查更新。", parent, err)
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Sprintf("准备更新目录 %s 失败：%v。请检查路径是否被文件占用及服务运行账号的目录权限，修复后重新检查更新。", s.dir, err)
	}
	if err := probeWritableDirectory(s.dir); err != nil {
		return fmt.Sprintf("更新目录 %s 的写入检测失败：%v。请检查服务运行账号的目录权限，修复后重新检查更新。", s.dir, err)
	}
	return ""
}

func probeWritableDirectory(dir string) error {
	f, err := os.CreateTemp(dir, ".easyrun-write-check-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	_, writeErr := f.Write([]byte{0})
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Remove(name)
}
