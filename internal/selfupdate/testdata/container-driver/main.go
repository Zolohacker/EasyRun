// This driver is compiled only by scripts/test-container-update.py. It injects
// an in-memory release into the real updater inside an isolated test container;
// it is not included in the application or any release archive.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Zolohacker/EasyRun/internal/buildinfo"
	"github.com/Zolohacker/EasyRun/internal/selfupdate"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fixtureGuard struct{}

func (fixtureGuard) Pause(string) (func(), error) { return func() {}, nil }

func die(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

func main() {
	if os.Getenv("EASYRUN_ISOLATED_CONTAINER_TEST") != "1" || len(os.Args) != 3 {
		die(fmt.Errorf("isolated container fixture only; args: binary release-version"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	response, err := http.Get("http://127.0.0.1:8080/healthz")
	if err != nil {
		die(err)
	}
	var health struct {
		Build buildinfo.Info `json:"build"`
		Face  bool           `json:"face_runtime_ready"`
	}
	err = json.NewDecoder(response.Body).Decode(&health)
	response.Body.Close()
	if err != nil {
		die(err)
	}
	buildinfo.Version, buildinfo.Commit, buildinfo.BuiltAt, buildinfo.BuildType = health.Build.Version, health.Build.Commit, health.Build.BuiltAt, health.Build.BuildType
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		die(err)
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "easyrun", Mode: 0755, Size: int64(len(data))}); err != nil {
		die(err)
	}
	if _, err := tw.Write(data); err != nil {
		die(err)
	}
	if err := tw.Close(); err != nil {
		die(err)
	}
	if err := gz.Close(); err != nil {
		die(err)
	}
	name := fmt.Sprintf("easyrun_v%s_linux_%s.tar.gz", os.Args[2], runtime.GOARCH)
	hash := sha256.Sum256(archive.Bytes())
	checksums := []byte(hex.EncodeToString(hash[:]) + "  " + name + "\n")
	release, _ := json.Marshal(selfupdate.Release{Tag: "v" + os.Args[2], Assets: []selfupdate.Asset{
		{ID: 1, Name: name, Size: int64(archive.Len())}, {ID: 2, Name: "checksums.txt", Size: int64(len(checksums))},
	}})
	client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		var body []byte
		switch r.URL.Path {
		case "/repos/" + selfupdate.Repository + "/releases/latest":
			body = release
		case "/repos/" + selfupdate.Repository + "/releases/assets/1":
			body = archive.Bytes()
		case "/repos/" + selfupdate.Repository + "/releases/assets/2":
			body = checksums
		default:
			return nil, fmt.Errorf("unexpected fixture request: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}
	os.Setenv("EASYRUN_SUPERVISED", "1")
	os.Setenv("EASYRUN_CONTAINER_MANAGED", "1")
	os.Setenv("EASYRUN_CONTAINER_RUNTIME", "/app/runtime")
	stopped := make(chan error, 1)
	s, err := selfupdate.New(ctx, selfupdate.Options{
		Executable: "/app/runtime/easyrun", HTTP: client, Guard: fixtureGuard{}, FaceReady: health.Face,
		Shutdown: func() { stopped <- stopFixtureServer() },
	})
	if err != nil {
		die(err)
	}
	if err := s.Check(ctx, true); err != nil {
		die(err)
	}
	status := s.Snapshot(ctx)
	if status.DeploymentMode != "container_managed" || !status.CanInstall {
		die(fmt.Errorf("managed update unavailable: %+v", status))
	}
	if err := s.Start(ctx, false); err != nil {
		die(err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-stopped:
			if err != nil {
				die(err)
			}
			fmt.Println(`{"accepted":true,"prepared":true}`)
			return
		case <-ticker.C:
			status := s.Snapshot(ctx)
			if status.Phase == "failed" {
				json.NewEncoder(os.Stdout).Encode(map[string]string{"phase": status.Phase, "message": status.Message})
				os.Exit(2)
			}
		case <-ctx.Done():
			die(ctx.Err())
		}
	}
}

func stopFixtureServer() error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		exe, _ := os.Readlink(filepath.Join("/proc", entry.Name(), "exe"))
		if strings.TrimSuffix(exe, " (deleted)") == "/app/runtime/easyrun" {
			p, err := os.FindProcess(pid)
			if err != nil {
				return err
			}
			return p.Signal(syscall.SIGTERM)
		}
	}
	return fmt.Errorf("fixture server not found")
}
