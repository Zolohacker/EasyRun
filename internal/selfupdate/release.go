package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const Repository = "Zolohacker/EasyRun"
const maxPackageSize int64 = 256 << 20

var stableTag = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}
type Release struct {
	ID          int64   `json:"id"`
	Tag         string  `json:"tag_name"`
	Name        string  `json:"name"`
	Notes       string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	Assets      []Asset `json:"assets"`
}

func compareVersion(a, b string) (int, error) {
	if !stableTag.MatchString(a) || !stableTag.MatchString(b) {
		return 0, errors.New("发行版本必须是稳定的三段版本号")
	}
	ap, bp := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := range ap {
		x, e := strconv.ParseUint(ap[i], 10, 64)
		if e != nil {
			return 0, e
		}
		y, e := strconv.ParseUint(bp[i], 10, 64)
		if e != nil {
			return 0, e
		}
		if x < y {
			return -1, nil
		}
		if x > y {
			return 1, nil
		}
	}
	return 0, nil
}
func archiveName(tag string) string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("easyrun_%s_%s_%s.%s", tag, runtime.GOOS, runtime.GOARCH, ext)
}
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "easyrun.exe"
	}
	return "easyrun"
}
func trustedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	}
	return false
}
func githubClient() *http.Client {
	return &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || !trustedURL(req.URL.String()) {
			return errors.New("更新下载重定向不在可信 GitHub 域名中")
		}
		if len(via) > 0 && req.URL.Hostname() != via[0].URL.Hostname() {
			req.Header.Del("Authorization")
		}
		return nil
	}}
}
func (s *Service) githubRequest(ctx context.Context, endpoint string, binary bool, limit int64) ([]byte, error) {
	address := "https://api.github.com/repos/" + Repository + endpoint
	r, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("User-Agent", "EasyRun-Updater")
	r.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	r.Header.Set("Accept", "application/vnd.github+json")
	if binary {
		r.Header.Set("Accept", "application/octet-stream")
	}
	resp, err := s.http.Do(r)
	if err != nil {
		return nil, errors.New("连接 GitHub 更新服务失败，请检查网络")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, errors.New("公开更新源尚无可用 Release，请等待维护者发布稳定版本")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub 更新服务返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, errors.New("更新下载超过大小上限")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("读取更新文件失败")
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("更新下载超过大小上限")
	}
	return raw, nil
}
func findAssets(r *Release) (Asset, Asset, error) {
	var archive, checksums Asset
	if !stableTag.MatchString(r.Tag) {
		return archive, checksums, errors.New("发行标签格式不正确")
	}
	for _, a := range r.Assets {
		if a.Name == archiveName(r.Tag) {
			if archive.ID != 0 {
				return archive, checksums, errors.New("发行包名称重复")
			}
			archive = a
		}
		if a.Name == "checksums.txt" {
			if checksums.ID != 0 {
				return archive, checksums, errors.New("校验文件名称重复")
			}
			checksums = a
		}
	}
	if archive.ID <= 0 || archive.Size <= 0 || archive.Size > maxPackageSize || checksums.ID <= 0 {
		return archive, checksums, errors.New("此发行版本缺少当前系统的安装包或 SHA-256 校验文件")
	}
	return archive, checksums, nil
}
func verifyChecksum(raw, checksums []byte, name string) error {
	actual := sha256.Sum256(raw)
	found := ""
	for _, line := range strings.Split(string(checksums), "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 || strings.TrimPrefix(parts[1], "*") != name {
			continue
		}
		if found != "" {
			return errors.New("校验文件存在重复条目")
		}
		found = strings.ToLower(parts[0])
	}
	if len(found) != 64 {
		return errors.New("安装包缺少有效的 SHA-256 校验值")
	}
	expected, err := hex.DecodeString(found)
	if err != nil || !equalBytes(expected, actual[:]) {
		return errors.New("安装包 SHA-256 校验失败")
	}
	return nil
}
func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
func safeEntry(name string) bool {
	name = strings.TrimSuffix(name, "/")
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "\\:\x00\r\n") && !strings.HasPrefix(name, "/") && !strings.HasPrefix(name, "../") && path.Clean(name) == name
}

// Never extract paths chosen by an archive. Only copy one regular root binary.
func extractArchive(archive, dest string) error {
	write := func(reader io.Reader, size int64) error {
		if size <= 0 || size > maxPackageSize {
			return errors.New("解压文件大小不正确")
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(f, io.LimitReader(reader, maxPackageSize+1))
		syncErr := f.Sync()
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != size {
			return errors.New("解压文件大小与清单不一致")
		}
		return nil
	}
	if strings.HasSuffix(archive, ".zip") {
		z, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer z.Close()
		if len(z.File) > 64 {
			return errors.New("压缩包文件过多")
		}
		var chosen *zip.File
		total := uint64(0)
		for _, f := range z.File {
			total += f.UncompressedSize64
			if !safeEntry(f.Name) || (!f.Mode().IsRegular() && !f.FileInfo().IsDir()) || f.UncompressedSize64 > uint64(maxPackageSize) || total > uint64(maxPackageSize) {
				return errors.New("压缩包含不安全路径、链接或过大文件")
			}
			if f.FileInfo().IsDir() {
				continue
			}
			if f.Name == binaryName() {
				if chosen != nil {
					return errors.New("压缩包存在重复程序")
				}
				chosen = f
			}
		}
		if chosen == nil {
			return errors.New("压缩包缺少 EasyRun 程序")
		}
		r, err := chosen.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		return write(r, int64(chosen.UncompressedSize64))
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	total := int64(0)
	for count := 0; ; count++ {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		total += h.Size
		if count >= 64 || !safeEntry(h.Name) || (h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir) || total > maxPackageSize {
			return errors.New("压缩包含不安全路径、链接或过大文件")
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Name == binaryName() {
			if found {
				return errors.New("压缩包存在重复程序")
			}
			if err := write(tr, h.Size); err != nil {
				return err
			}
			found = true
		}
	}
	if !found {
		return errors.New("压缩包缺少 EasyRun 程序")
	}
	return nil
}
func fileHash(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func writeJSONAtomic(name string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temp := name + ".tmp"
	f, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, e := f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	if c := f.Close(); e == nil {
		e = c
	}
	if e != nil {
		return e
	}
	return replaceStateFile(temp, name)
}
func packagePath(dir, name string) string { return filepath.Join(dir, name) }
