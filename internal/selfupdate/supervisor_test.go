package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zolohacker/EasyRun/internal/buildinfo"
)

func TestHealthCheckRequiresExactBuildAndBootIdentity(t *testing.T) {
	info := buildinfo.Info{Version: "1.0.0", Commit: "fixture"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "boot_id": "boot", "build": info})
	}))
	defer server.Close()
	done := make(chan error)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := verifyHealth(ctx, server.URL+"/healthz", "boot", info, done); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel2()
	if verifyHealth(ctx2, server.URL+"/healthz", "wrong", info, done) == nil {
		t.Fatal("wrong process accepted")
	}
	if _, err := HealthURL("198.51.100.1:8080"); err == nil {
		t.Fatal("nonloopback check accepted")
	}
	if got, err := HealthURL(":8080"); err != nil || got != "http://127.0.0.1:8080/healthz" {
		t.Fatalf("%s %v", got, err)
	}
}

func TestReplaceAndRestoreDoNotMutateOnHashMismatch(t *testing.T) {
	dir := t.TempDir()
	state := State{Target: filepath.Join(dir, "app"), Candidate: filepath.Join(dir, "candidate"), Backup: filepath.Join(dir, "previous")}
	_ = os.WriteFile(state.Target, []byte("old"), 0755)
	_ = os.WriteFile(state.Candidate, []byte("new"), 0755)
	state.BeforeSHA, _ = fileHash(state.Target)
	state.AfterSHA, _ = fileHash(state.Candidate)
	good := state.AfterSHA
	state.AfterSHA = "invalid"
	if replaceBinary(state) == nil {
		t.Fatal("tampered candidate installed")
	}
	data, _ := os.ReadFile(state.Target)
	if string(data) != "old" {
		t.Fatal("target changed")
	}
	state.AfterSHA = good
	if err := replaceBinary(state); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(state.Target)
	if string(data) != "new" {
		t.Fatal("not replaced")
	}
	backup, err := readBackup(dir)
	if err != nil || backup.SHA != state.BeforeSHA {
		t.Fatalf("backup: %v", err)
	}
	if err := restoreBinary(state); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(state.Target)
	if string(data) != "old" {
		t.Fatal("not restored")
	}
}

const fixtureProgram = `package main
import("encoding/json";"net/http";"os";"runtime";"time")
var version string
func main(){
info:=map[string]any{"version":version,"commit":version,"built_at":"fixture","build_type":"release","os":runtime.GOOS,"arch":runtime.GOARCH,"native_face":true}
if len(os.Args)>1&&os.Args[1]=="--version-json"{if os.Getenv("DATABASE_URL")!=""||os.Getenv("ACCOUNT_MASTER_KEY")!=""{os.Exit(9)};_ =json.NewEncoder(os.Stdout).Encode(info);return}
if version=="3.0.0"{os.Exit(3)}
http.HandleFunc("/healthz",func(w http.ResponseWriter,r *http.Request){_ =json.NewEncoder(w).Encode(map[string]any{"status":"ok","boot_id":os.Getenv("EASYRUN_BOOT_ID"),"build":info})})
http.HandleFunc("/quit",func(w http.ResponseWriter,r *http.Request){w.WriteHeader(200);go func(){time.Sleep(100*time.Millisecond);os.Exit(0)}()})
_ =http.ListenAndServe(os.Getenv("HTTP_ADDR"),nil)
}`

// This is a real process/executable replacement test, including Windows EXE
// locks. It never starts EasyRun, opens a database or touches production files.
func TestSupervisorLiveUpgradeAndAutomaticRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("builds standalone process fixtures")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(source, []byte(fixtureProgram), 0600); err != nil {
		t.Fatal(err)
	}
	build := func(version string) string {
		out := filepath.Join(dir, "fixture-"+version+filepath.Ext(binaryName()))
		cmd := exec.Command("go", "build", "-buildvcs=false", "-ldflags=-X main.version="+version, "-o", out, source)
		configureChild(cmd)
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build fixture: %v %s", err, data)
		}
		return out
	}
	old, next, bad := build("1.0.0"), build("2.0.0"), build("3.0.0")
	// Fetch a synthetic release through the real downloader/verification path,
	// then probe its metadata without any inherited application credentials.
	t.Setenv("DATABASE_URL", "fixture-secret-must-not-reach-candidate")
	format := "tar.gz"
	if filepath.Ext(binaryName()) == ".exe" {
		format = "zip"
	}
	contents, e := os.ReadFile(next)
	if e != nil {
		t.Fatal(e)
	}
	archive := makeArchive(t, format, []string{binaryName()}, 0755, contents)
	release := &Release{Tag: "v2.0.0", Assets: []Asset{{ID: 1, Name: archiveName("v2.0.0"), Size: int64(len(archive))}, {ID: 2, Name: "checksums.txt", Size: 100}}}
	stageDir := t.TempDir()
	candidate := filepath.Join(stageDir, "candidate"+filepath.Ext(binaryName()))
	svc := &Service{dir: stageDir, http: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/repos/"+Repository+"/releases/assets/1" {
			return response(200, archive), nil
		}
		return response(200, checksum(archive, archiveName(release.Tag))), nil
	})}}
	manifest := State{Candidate: candidate}
	if err := svc.prepare(context.Background(), release, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.After.Version != "2.0.0" || manifest.AfterSHA == "" {
		t.Fatal("downloaded release not verified")
	}
	target := filepath.Join(dir, binaryName())
	if err := copyFile(old, target, 0755); err != nil {
		t.Fatal(err)
	}
	updateDir := filepath.Join(dir, ".easyrun-update")
	if err := os.MkdirAll(updateDir, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	t.Setenv("HTTP_ADDR", addr)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Supervise(ctx, target, "http://"+addr+"/healthz") }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Errorf("supervisor: %v", e)
			}
		case <-time.After(15 * time.Second):
			t.Error("supervisor did not stop")
		}
		_ = waitWritable(target, 5*time.Second)
	}()
	wait := func(message string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal(message)
	}
	client := &http.Client{Timeout: time.Second}
	healthVersion := func() string {
		resp, e := client.Get("http://" + addr + "/healthz")
		if e != nil {
			return ""
		}
		defer resp.Body.Close()
		var v struct {
			Build buildinfo.Info `json:"build"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&v)
		return v.Build.Version
	}
	wait("original fixture never started", func() bool { return healthVersion() == "1.0.0" })
	stage := func(from, to, fixture string) {
		state := State{Phase: "prepared", ReleaseTag: "v" + to, Target: target, Candidate: filepath.Join(updateDir, "candidate"+filepath.Ext(binaryName())), Backup: filepath.Join(updateDir, "previous"+filepath.Ext(binaryName())), Before: buildinfo.Info{Version: from, Commit: from}, After: buildinfo.Info{Version: to, Commit: to}, UpdatedAt: time.Now().UTC()}
		if e := copyFile(fixture, state.Candidate, 0755); e != nil {
			t.Fatal(e)
		}
		state.BeforeSHA, _ = fileHash(target)
		state.AfterSHA, _ = fileHash(state.Candidate)
		if !statePathsValid(state, target, updateDir) {
			t.Fatal("fixture paths invalid")
		}
		if e := writeJSONAtomic(filepath.Join(updateDir, "state.json"), state); e != nil {
			t.Fatal(e)
		}
		resp, e := client.Post("http://"+addr+"/quit", "application/json", nil)
		if e != nil {
			t.Fatal(e)
		}
		_ = resp.Body.Close()
	}
	stage("1.0.0", "2.0.0", next)
	wait("live executable upgrade failed", func() bool {
		s, e := readState(filepath.Join(updateDir, "state.json"))
		return e == nil && s.Phase == "succeeded" && healthVersion() == "2.0.0"
	})
	info, err := inspectCandidate(context.Background(), next)
	if err != nil || info.Version != "2.0.0" {
		t.Fatalf("candidate probe: %+v %v", info, err)
	}
	stage("2.0.0", "3.0.0", bad)
	wait("failed executable was not automatically rolled back", func() bool {
		s, e := readState(filepath.Join(updateDir, "state.json"))
		return e == nil && s.Phase == "rolled_back" && healthVersion() == "2.0.0"
	})
	got, _ := fileHash(target)
	want, _ := fileHash(next)
	if got != want {
		t.Fatal("rollback target hash differs")
	}
	metadata, e := readBackup(updateDir)
	if e != nil || metadata.Build.Version != "2.0.0" {
		t.Fatalf("backup metadata: %+v %v", metadata, e)
	}
	t.Log(fmt.Sprintf("real %s: 1.0.0 -> 2.0.0 verified; bad 3.0.0 -> 2.0.0 automatically restored", buildinfo.Current().OS))
}
