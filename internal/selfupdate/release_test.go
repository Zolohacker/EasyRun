package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zolohacker/EasyRun/internal/buildinfo"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}
}
func checksum(data []byte, name string) []byte {
	h := sha256.Sum256(data)
	return []byte(hex.EncodeToString(h[:]) + "  " + name + "\n")
}

func TestStableVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"1.2.9", "v1.3.0", -1}, {"v2.0.0", "1.99.99", 1}, {"1.0.0", "v1.0.0", 0}} {
		got, err := compareVersion(tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Fatalf("%+v: %d %v", tc, got, err)
		}
	}
	for _, bad := range []string{"1.2", "v01.2.3", "1.2.3-beta", "../1.2.3", "1.2.3+dev"} {
		if _, err := compareVersion(bad, "1.2.3"); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestAutomaticUpdateRespectsFailuresAndManualRollback(t *testing.T) {
	for _, s := range []State{{Phase: "failed", ReleaseTag: "v2.0.0"}, {Phase: "rolled_back", ReleaseTag: "2.0.0"}, {Phase: "succeeded", Mode: "rollback", Before: buildinfo.Info{Version: "2.0.0"}}} {
		if !autoSuppressed(s, "v2.0.0") {
			t.Fatalf("same bad/rolled-back version retried: %+v", s)
		}
		if autoSuppressed(s, "v2.0.1") {
			t.Fatal("future release blocked")
		}
	}
}
func TestMandatoryUniqueChecksums(t *testing.T) {
	raw := []byte("release package")
	good := checksum(raw, "package.zip")
	if err := verifyChecksum(raw, good, "package.zip"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{nil, []byte("broken package.zip"), checksum([]byte("tampered"), "package.zip"), append(append([]byte{}, good...), good...)} {
		if err := verifyChecksum(raw, bad, "package.zip"); err == nil {
			t.Fatal("accepted invalid checksum")
		}
	}
}
func makeArchive(t *testing.T, format string, names []string, mode os.FileMode, content []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if format == "zip" {
		w := zip.NewWriter(&b)
		for _, name := range names {
			h := &zip.FileHeader{Name: name, Method: zip.Deflate}
			h.SetMode(mode)
			f, e := w.CreateHeader(h)
			if e != nil {
				t.Fatal(e)
			}
			_, _ = f.Write(content)
		}
		if e := w.Close(); e != nil {
			t.Fatal(e)
		}
	} else {
		gz := gzip.NewWriter(&b)
		w := tar.NewWriter(gz)
		for _, name := range names {
			kind := byte(tar.TypeReg)
			if mode&os.ModeSymlink != 0 {
				kind = tar.TypeSymlink
			}
			if e := w.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(content)), Typeflag: kind, Linkname: "outside"}); e != nil {
				t.Fatal(e)
			}
			if kind == tar.TypeReg {
				_, _ = w.Write(content)
			}
		}
		_ = w.Close()
		_ = gz.Close()
	}
	return b.Bytes()
}
func TestArchiveExtractionIsBoundedAndPathSafe(t *testing.T) {
	for _, format := range []string{"zip", "tar.gz"} {
		t.Run(format, func(t *testing.T) {
			for _, tc := range []struct {
				name  string
				names []string
				mode  os.FileMode
				ok    bool
			}{
				{"valid", []string{binaryName()}, 0755, true},
				{"full release keeps models untouched", []string{binaryName(), "models/Megatron", "models/linux-amd64/libInspireFace.so"}, 0755, true},
				{"traversal", []string{"../" + binaryName()}, 0755, false},
				{"absolute", []string{"/" + binaryName()}, 0755, false},
				{"windows path", []string{"C:\\" + binaryName()}, 0755, false},
				{"symlink", []string{binaryName()}, os.ModeSymlink | 0755, false},
				{"duplicate", []string{binaryName(), binaryName()}, 0755, false},
				{"missing", []string{"README.md"}, 0644, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					dir := t.TempDir()
					name := filepath.Join(dir, "release."+format)
					_ = os.WriteFile(name, makeArchive(t, format, tc.names, tc.mode, []byte("fixture")), 0600)
					out := filepath.Join(dir, "candidate")
					err := extractArchive(name, out)
					if (err == nil) != tc.ok {
						t.Fatalf("accepted=%v err=%v", err == nil, err)
					}
					if tc.ok {
						data, _ := os.ReadFile(out)
						if string(data) != "fixture" {
							t.Fatal("bad extraction")
						}
					}
				})
			}
		})
	}
}
func TestReleaseAssetsAreExactAndUnique(t *testing.T) {
	r := Release{Tag: "v1.2.3", Assets: []Asset{{ID: 1, Name: archiveName("v1.2.3"), Size: 99}, {ID: 2, Name: "checksums.txt", Size: 99}}}
	if _, _, err := findAssets(&r); err != nil {
		t.Fatal(err)
	}
	r.Assets = append(r.Assets, r.Assets[1])
	if _, _, err := findAssets(&r); err == nil {
		t.Fatal("duplicate checksums accepted")
	}
	r.Assets = r.Assets[:1]
	if _, _, err := findAssets(&r); err == nil {
		t.Fatal("missing checksums accepted")
	}
}
func TestGitHubTrustAndCredentialStripping(t *testing.T) {
	for _, bad := range []string{"http://api.github.com/", "https://api.github.com.evil.test/", "https://user@api.github.com/", "https://api.github.com:444/", "https://127.0.0.1/"} {
		if trustedURL(bad) {
			t.Fatalf("trusted %s", bad)
		}
	}
	first, _ := http.NewRequest("GET", "https://api.github.com/asset", nil)
	next, _ := http.NewRequest("GET", "https://release-assets.githubusercontent.com/asset", nil)
	next.Header.Set("Authorization", "Bearer fixture-secret")
	if err := githubClient().CheckRedirect(next, []*http.Request{first}); err != nil {
		t.Fatal(err)
	}
	if next.Header.Get("Authorization") != "" {
		t.Fatal("cross-host token leak")
	}
	next.URL, _ = url.Parse("https://evil.test/asset")
	if githubClient().CheckRedirect(next, []*http.Request{first}) == nil {
		t.Fatal("untrusted redirect accepted")
	}
}
func TestCheckCachesAndNeverTreatsFailureAsUpToDate(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "fixture-ignored-token")
	t.Setenv("GH_TOKEN", "fixture-ignored-token")
	calls := 0
	s := &Service{http: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Fatal("public updater added credentials")
		}
		if r.URL.Host != "api.github.com" || r.URL.Path != "/repos/"+Repository+"/releases/latest" {
			t.Fatalf("unexpected URL %s", r.URL)
		}
		if calls > 1 {
			return response(404, nil), nil
		}
		b, _ := json.Marshal(Release{Tag: "v999.0.0", Assets: []Asset{{ID: 1, Name: archiveName("v999.0.0"), Size: 12}, {ID: 2, Name: "checksums.txt", Size: 99}}})
		return response(200, b), nil
	})}}
	if err := s.Check(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !s.status.HasUpdate {
		t.Fatal("missing update")
	}
	_ = s.Check(context.Background(), false)
	if calls != 1 {
		t.Fatal("cache missed")
	}
	if err := s.Check(context.Background(), true); err == nil {
		t.Fatal("missing failure")
	}
	if s.status.HasUpdate || s.latest != nil || s.status.LastError == "" {
		t.Fatal("failure was treated as valid update")
	}
}
func TestCandidateMetadataAndEnvironment(t *testing.T) {
	current := buildinfo.Info{Version: "1.0.0", OS: "linux", Arch: "amd64", NativeFace: true}
	good := buildinfo.Info{Version: "2.0.0", Commit: "abc123", BuiltAt: "2026-10-23", BuildType: "release", OS: "linux", Arch: "amd64", NativeFace: true}
	if err := validateBuild(good, "v2.0.0", current); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*buildinfo.Info){func(v *buildinfo.Info) { v.OS = "windows" }, func(v *buildinfo.Info) { v.Arch = "arm64" }, func(v *buildinfo.Info) { v.NativeFace = false }, func(v *buildinfo.Info) { v.Version = "1.0.0" }, func(v *buildinfo.Info) { v.BuildType = "source" }} {
		bad := good
		change(&bad)
		if validateBuild(bad, "v2.0.0", current) == nil {
			t.Fatal("invalid candidate accepted")
		}
	}
	t.Setenv("DATABASE_URL", "secret")
	t.Setenv("ACCOUNT_MASTER_KEY", "secret")
	t.Setenv("GITHUB_TOKEN", "secret")
	for _, v := range validationEnv() {
		if strings.Contains(v, "secret") || strings.HasPrefix(v, "DATABASE_URL=") {
			t.Fatal("credential inherited by candidate probe")
		}
	}
}
