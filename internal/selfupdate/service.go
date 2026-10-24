package selfupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zolohacker/EasyRun/internal/buildinfo"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pauser interface{ Pause(string) (func(), error) }
type Settings struct {
	AutoInstall bool   `json:"auto_install"`
	Repository  string `json:"repository"`
}
type State struct {
	RequireFace bool           `json:"require_face"`
	ReleaseTag  string         `json:"release_tag"`
	Phase       string         `json:"phase"`
	Message     string         `json:"message"`
	Mode        string         `json:"mode"`
	Target      string         `json:"target"`
	Candidate   string         `json:"candidate"`
	Backup      string         `json:"backup"`
	BeforeSHA   string         `json:"before_sha"`
	AfterSHA    string         `json:"after_sha"`
	Before      buildinfo.Info `json:"before"`
	After       buildinfo.Info `json:"after"`
	UpdatedAt   time.Time      `json:"updated_at"`
}
type backupInfo struct {
	SHA   string         `json:"sha"`
	Build buildinfo.Info `json:"build"`
}
type Status struct {
	Current          buildinfo.Info `json:"current"`
	LatestVersion    string         `json:"latest_version"`
	HasUpdate        bool           `json:"has_update"`
	Notes            string         `json:"notes"`
	PublishedAt      string         `json:"published_at"`
	CheckedAt        *time.Time     `json:"checked_at,omitempty"`
	LastError        string         `json:"last_error"`
	CanInstall       bool           `json:"can_install"`
	CanRollback      bool           `json:"can_rollback"`
	CapabilityReason string         `json:"capability_reason"`
	DeploymentMode   string         `json:"deployment_mode"`
	Phase            string         `json:"phase"`
	Message          string         `json:"message"`
	Settings         Settings       `json:"settings"`
}
type Options struct {
	FaceReady  bool
	DB         *pgxpool.Pool
	Guard      Pauser
	Busy       func() bool
	Shutdown   func()
	Executable string
	HTTP       *http.Client
}
type Service struct {
	requireFace                     bool
	db                              *pgxpool.Pool
	guard                           Pauser
	workloadBusy                    func() bool
	shutdown                        func()
	http                            *http.Client
	exe, dir, statePath, capability string
	mu                              sync.Mutex
	checkMu                         sync.Mutex
	installMu                       sync.Mutex
	status                          Status
	latest                          *Release
	bootRelease                     func()
}

func New(ctx context.Context, o Options) (*Service, error) {
	exe := o.Executable
	var err error
	if exe == "" {
		exe, err = os.Executable()
		if err != nil {
			return nil, err
		}
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return nil, err
	}
	s := &Service{db: o.DB, requireFace: o.FaceReady, guard: o.Guard, workloadBusy: o.Busy, shutdown: o.Shutdown, http: o.HTTP, exe: exe, dir: filepath.Join(filepath.Dir(exe), ".easyrun-update")}
	s.statePath = filepath.Join(s.dir, "state.json")
	if s.http == nil {
		s.http = githubClient()
	}
	s.status.Current = buildinfo.Current()
	if o.DB != nil {
		if _, err := o.DB.Exec(ctx, `CREATE TABLE IF NOT EXISTS platform_update_settings(id smallint PRIMARY KEY CHECK(id=1),auto_install boolean NOT NULL DEFAULT false); INSERT INTO platform_update_settings(id)VALUES(1)ON CONFLICT DO NOTHING;
DO $$ BEGIN IF EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='platform_update_settings' AND column_name='token_cipher') THEN EXECUTE 'UPDATE platform_update_settings SET token_cipher=NULL'; END IF; END $$;`); err != nil {
			return nil, err
		}
	}
	supervised, container, managed := os.Getenv("EASYRUN_SUPERVISED") == "1", runningInContainer(), managedContainerRuntime(s.exe)
	s.capability = deploymentCapability(supervised, container, managed)
	s.status.DeploymentMode = "standalone"
	if supervised {
		s.status.DeploymentMode = "supervised"
	}
	if container {
		s.status.DeploymentMode = "container"
		if supervised && managed {
			s.status.DeploymentMode = "container_managed"
		}
	}
	_ = s.installCapability()
	if state, e := readState(s.statePath); e == nil && state.Target == s.exe && state.Phase == "downloading" {
		state.Phase, state.Message = "failed", "上次准备更新被中断，可重新发起更新"
		state.UpdatedAt = time.Now().UTC()
		_ = writeJSONAtomic(s.statePath, state)
	}
	if state, err := readState(s.statePath); err == nil && statePathsValid(state, s.exe, s.dir) && (state.Phase == "prepared" || state.Phase == "installing" || state.Phase == "verifying") && s.guard != nil {
		var pauseErr error
		s.bootRelease, pauseErr = s.guard.Pause("正在验收平台更新，请稍候")
		if pauseErr != nil {
			return nil, fmt.Errorf("更新验收门禁尚未锁定：%w", pauseErr)
		}
	}
	return s, nil
}
func (s *Service) settings(ctx context.Context) (Settings, error) {
	v := Settings{Repository: Repository}
	if s.db == nil {
		return v, nil
	}
	err := s.db.QueryRow(ctx, `SELECT auto_install FROM platform_update_settings WHERE id=1`).Scan(&v.AutoInstall)
	return v, err
}
func (s *Service) SaveSettings(ctx context.Context, auto bool) error {
	if s.db == nil {
		return errors.New("更新配置存储未初始化")
	}
	if auto {
		if reason := s.installCapability(); reason != "" {
			return errors.New(reason)
		}
	}
	_, err := s.db.Exec(ctx, `UPDATE platform_update_settings SET auto_install=$1 WHERE id=1`, auto)
	if err == nil {
		s.mu.Lock()
		s.status.CheckedAt = nil
		s.mu.Unlock()
	}
	return err
}
func (s *Service) Snapshot(ctx context.Context) Status {
	s.mu.Lock()
	v := s.status
	s.mu.Unlock()
	v.Settings, _ = s.settings(ctx)
	v.CapabilityReason = s.installCapability()
	v.CanInstall = v.CapabilityReason == "" && v.HasUpdate
	state, err := readState(s.statePath)
	if err == nil && state.Target == s.exe {
		v.Phase, v.Message = state.Phase, state.Message
	}
	if backup, err := readBackup(s.dir); err == nil {
		_, statErr := os.Stat(filepath.Join(s.dir, "previous"+filepath.Ext(binaryName())))
		v.CanRollback = v.CapabilityReason == "" && statErr == nil && (backup.Build.Version != buildinfo.Version || backup.Build.Commit != buildinfo.Commit)
	}
	if v.Phase == "downloading" || v.Phase == "prepared" || v.Phase == "installing" || v.Phase == "verifying" {
		v.CanInstall, v.CanRollback = false, false
	}
	return v
}
func (s *Service) Check(ctx context.Context, force bool) error {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	s.mu.Lock()
	if !force && s.status.CheckedAt != nil && time.Since(*s.status.CheckedAt) < 20*time.Minute {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	_, err := s.settings(ctx)
	var release Release
	if err == nil {
		var raw []byte
		raw, err = s.githubRequest(ctx, "/releases/latest", false, 2<<20)
		if err == nil {
			err = json.Unmarshal(raw, &release)
		}
	}
	if err == nil && (release.Draft || release.Prerelease || !stableTag.MatchString(release.Tag)) {
		err = errors.New("更新源没有可用的稳定发行版本")
	}
	if err == nil {
		_, _, err = findAssets(&release)
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.CheckedAt = &now
	if err != nil {
		s.status.LastError = err.Error()
		s.status.HasUpdate = false
		s.latest = nil
		return err
	}
	cmp, err := compareVersion(buildinfo.Version, release.Tag)
	if err != nil {
		s.status.LastError = err.Error()
		return err
	}
	s.latest = &release
	s.status.LatestVersion = release.Tag
	s.status.HasUpdate = cmp < 0
	s.status.LastError = ""
	s.status.Notes = release.Notes
	s.status.PublishedAt = release.PublishedAt
	return nil
}
func (s *Service) Start(ctx context.Context, rollback bool) error {
	if reason := s.installCapability(); reason != "" {
		return errors.New(reason)
	}
	if !s.installMu.TryLock() {
		return errors.New("平台更新正在进行")
	}
	fail := func(err error) error { s.installMu.Unlock(); return err }
	if s.guard == nil || s.shutdown == nil {
		return fail(errors.New("更新守护组件尚未配置"))
	}
	if !rollback {
		if err := s.Check(ctx, true); err != nil {
			return fail(err)
		}
	}
	s.mu.Lock()
	release := s.latest
	s.mu.Unlock()
	if !rollback && (release == nil || !s.Snapshot(ctx).HasUpdate) {
		return fail(errors.New("当前没有可安装的新发行版本"))
	}
	releasePause, err := s.guard.Pause("正在准备平台更新")
	if err != nil {
		return fail(err)
	}
	if s.workloadBusy != nil && s.workloadBusy() {
		releasePause()
		return fail(errors.New("仍有运行中或排队任务，请在空闲时更新"))
	}
	before, err := fileHash(s.exe)
	if err != nil {
		releasePause()
		return fail(err)
	}
	state := State{Phase: "downloading", Message: "正在下载并校验发行包", Mode: "update", Target: s.exe, Candidate: filepath.Join(s.dir, "candidate"+filepath.Ext(binaryName())), Backup: filepath.Join(s.dir, "previous"+filepath.Ext(binaryName())), BeforeSHA: before, Before: buildinfo.Current(), UpdatedAt: time.Now().UTC()}
	state.RequireFace = s.requireFace
	if release != nil {
		state.ReleaseTag = release.Tag
	}
	if rollback {
		previous, e := readBackup(s.dir)
		if e != nil {
			releasePause()
			return fail(errors.New("没有可回滚的更新备份"))
		}
		hash, e := fileHash(state.Backup)
		if e != nil || hash != previous.SHA {
			releasePause()
			return fail(errors.New("回滚备份缺失或校验失败"))
		}
		state.Mode, state.Message, state.After, state.AfterSHA, state.ReleaseTag = "rollback", "正在准备已验证的回滚备份", previous.Build, previous.SHA, previous.Build.Version
		if err = copyFile(state.Backup, state.Candidate, 0755); err != nil {
			releasePause()
			return fail(err)
		}
	}
	if err := writeJSONAtomic(s.statePath, state); err != nil {
		releasePause()
		return fail(err)
	}
	go func() {
		defer s.installMu.Unlock()
		background, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		err := error(nil)
		if !rollback {
			err = s.prepare(background, release, &state)
		}
		if err == nil {
			state.Phase, state.Message = "prepared", "安装包已校验，正在重启平台"
			state.UpdatedAt = time.Now().UTC()
			err = writeJSONAtomic(s.statePath, state)
		}
		if err != nil {
			state.Phase, state.Message = "failed", err.Error()
			state.UpdatedAt = time.Now().UTC()
			_ = writeJSONAtomic(s.statePath, state)
			releasePause()
			return
		}
		// The supervisor owns replacement, health verification and rollback.
		// Keep the pause held until this process shuts down.
		s.shutdown()
	}()
	return nil
}
func (s *Service) prepare(ctx context.Context, release *Release, state *State) error {
	archive, checksums, err := findAssets(release)
	if err != nil {
		return err
	}
	raw, err := s.githubRequest(ctx, fmt.Sprintf("/releases/assets/%d", archive.ID), true, maxPackageSize)
	if err != nil {
		return err
	}
	checks, err := s.githubRequest(ctx, fmt.Sprintf("/releases/assets/%d", checksums.ID), true, 256<<10)
	if err != nil {
		return err
	}
	if err := verifyChecksum(raw, checks, archive.Name); err != nil {
		return err
	}
	archivePath := packagePath(s.dir, archive.Name)
	if err := os.WriteFile(archivePath, raw, 0600); err != nil {
		return err
	}
	defer os.Remove(archivePath)
	_ = os.Remove(state.Candidate) // A fixed staging file within the verified executable directory.
	if err := extractArchive(archivePath, state.Candidate); err != nil {
		return err
	}
	info, err := inspectCandidate(ctx, state.Candidate)
	if err != nil {
		return err
	}
	if err := validateBuild(info, release.Tag, buildinfo.Current()); err != nil {
		return err
	}
	if s.status.DeploymentMode == "container_managed" && info.ContainerRuntime != buildinfo.Current().ContainerRuntime {
		return errors.New("发行包与当前容器守护协议不兼容，请通过更新镜像升级；当前程序保持不变")
	}
	state.After = info
	state.AfterSHA, err = fileHash(state.Candidate)
	return err
}

func validateBuild(info buildinfo.Info, tag string, current buildinfo.Info) error {
	if info.OS != current.OS || info.Arch != current.Arch {
		return errors.New("安装包系统或架构不匹配")
	}
	if current.NativeFace && !info.NativeFace {
		return errors.New("发行包缺少当前平台需要的本地人脸推理支持")
	}
	if cmp, err := compareVersion(info.Version, tag); err != nil || cmp != 0 {
		return errors.New("程序内置版本与发行标签不匹配")
	}
	if info.Commit == "" || info.BuiltAt == "" || info.BuildType != "release" {
		return errors.New("发行包构建元数据不完整")
	}
	return nil
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 64<<10 {
		return 0, errors.New("版本信息输出过大")
	}
	return b.Buffer.Write(p)
}
func inspectCandidate(ctx context.Context, name string) (buildinfo.Info, error) {
	var info buildinfo.Info
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probe, name, "--version-json")
	cmd.Dir = filepath.Dir(name)
	cmd.Env = validationEnv()
	configureChild(cmd)
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return info, errors.New("发行程序版本自检失败")
	}
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		return info, errors.New("发行程序未提供有效版本元数据")
	}
	return info, nil
}
func validationEnv() []string {
	var env []string
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "SystemDrive", "LANG"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}
func (s *Service) Run(ctx context.Context) {
	// Version discovery must never delay the supervisor's boot-maintenance
	// release (GitHub may be temporarily unreachable).
	go func() {
		_ = s.Check(ctx, false)
		checks := time.NewTicker(20 * time.Minute)
		defer checks.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-checks.C:
				_ = s.Check(ctx, false)
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastAuto := time.Time{}
	for {
		if s.bootRelease != nil {
			if state, err := readState(s.statePath); err == nil && state.Target == s.exe && (state.Phase == "succeeded" || state.Phase == "rolled_back" || state.Phase == "failed") {
				s.bootRelease()
				s.bootRelease = nil
			}
		}
		if time.Since(lastAuto) >= time.Minute {
			lastAuto = time.Now()
			cfg, err := s.settings(ctx)
			v := s.Snapshot(ctx)
			state, _ := readState(s.statePath)
			if err == nil && cfg.AutoInstall && v.CanInstall && !autoSuppressed(state, v.LatestVersion) {
				_ = s.Start(ctx, false)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func autoSuppressed(state State, latest string) bool {
	same := func(a, b string) bool { return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v") }
	if (state.Phase == "rolled_back" || state.Phase == "failed") && same(state.ReleaseTag, latest) {
		return true
	}
	// A deliberate rollback must not be immediately undone by idle auto-update.
	return state.Mode == "rollback" && state.Phase == "succeeded" && same(state.Before.Version, latest)
}
