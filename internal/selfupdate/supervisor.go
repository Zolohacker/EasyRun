package selfupdate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zolohacker/EasyRun/internal/buildinfo"
)

func readState(name string) (State, error) {
	var s State
	raw, err := os.ReadFile(name)
	if err != nil {
		return s, err
	}
	if len(raw) > 128<<10 {
		return s, errors.New("更新状态文件过大")
	}
	err = json.Unmarshal(raw, &s)
	return s, err
}
func readBackup(dir string) (backupInfo, error) {
	var s backupInfo
	raw, err := os.ReadFile(filepath.Join(dir, "backup.json"))
	if err != nil {
		return s, err
	}
	if len(raw) > 64<<10 {
		return s, errors.New("备份元数据过大")
	}
	err = json.Unmarshal(raw, &s)
	if err == nil && len(s.SHA) != 64 {
		err = errors.New("备份校验信息不正确")
	}
	return s, err
}
func copyFile(from, to string, mode os.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if e := out.Close(); err == nil {
		err = e
	}
	return err
}
func statePathsValid(state State, target, dir string) bool {
	return state.Target == target && state.Candidate == filepath.Join(dir, "candidate"+filepath.Ext(binaryName())) && state.Backup == filepath.Join(dir, "previous"+filepath.Ext(binaryName())) && len(state.BeforeSHA) == 64 && len(state.AfterSHA) == 64
}
func HealthURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", errors.New("内置更新验收需绑定回环地址或所有网卡")
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}
func startServer(target string) (*exec.Cmd, <-chan error, string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, nil, "", err
	}
	id := hex.EncodeToString(nonce[:])
	cmd := exec.Command(target)
	cmd.Dir = ""
	configureChild(cmd)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "EASYRUN_SUPERVISED=") && !strings.HasPrefix(entry, "EASYRUN_BOOT_ID=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "EASYRUN_SUPERVISED=1", "EASYRUN_BOOT_ID="+id)
	if err := cmd.Start(); err != nil {
		return nil, nil, "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	return cmd, done, id, nil
}
func stopChild(cmd *exec.Cmd, done <-chan error) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}
func verifyHealth(ctx context.Context, address, bootID string, info buildinfo.Info, done <-chan error) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.Path != "/healthz" {
		return errors.New("更新健康检查地址不正确")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("更新健康检查只访问本机")
	}
	httpClient := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.New("新版本健康检查超时")
		case err := <-done:
			return fmt.Errorf("新版本启动退出：%v", err)
		case <-ticker.C:
			r, err := httpClient.Get(address)
			if err != nil {
				continue
			}
			var body struct {
				Status string         `json:"status"`
				BootID string         `json:"boot_id"`
				Build  buildinfo.Info `json:"build"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
			_ = r.Body.Close()
			if decodeErr == nil && r.StatusCode == 200 && body.Status == "ok" && body.BootID == bootID && body.Build.Version == info.Version && body.Build.Commit == info.Commit {
				return nil
			}
		}
	}
}
func replaceBinary(state State) error {
	current, err := fileHash(state.Target)
	if err != nil || current != state.BeforeSHA {
		return errors.New("当前程序与更新前校验值不一致，停止替换")
	}
	next, err := fileHash(state.Candidate)
	if err != nil || next != state.AfterSHA {
		return errors.New("待安装程序校验失败")
	}
	if err := waitWritable(state.Target, 20*time.Second); err != nil {
		return err
	}
	backupTemp := state.Backup + ".tmp"
	if err := copyFile(state.Target, backupTemp, 0755); err != nil {
		return err
	}
	if err := replaceStateFile(backupTemp, state.Backup); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(filepath.Dir(state.Backup), "backup.json"), backupInfo{state.BeforeSHA, state.Before}); err != nil {
		return err
	}
	return replaceStateFile(state.Candidate, state.Target)
}
func restoreBinary(state State) error {
	hash, err := fileHash(state.Backup)
	if err != nil || hash != state.BeforeSHA {
		return errors.New("回滚备份校验失败")
	}
	if err := waitWritable(state.Target, 20*time.Second); err != nil {
		return err
	}
	temp := state.Target + ".rollback-next"
	if err := copyFile(state.Backup, temp, 0755); err != nil {
		return err
	}
	return replaceStateFile(temp, state.Target)
}

// The supervisor runs from a separate executable so Windows never has to
// replace a running EXE. It keeps the same environment and working directory.
func Supervise(ctx context.Context, target, address string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	target, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return err
	}
	self, _ := os.Executable()
	self, _ = filepath.EvalSymlinks(self)
	if strings.EqualFold(self, target) {
		return errors.New("守护程序需使用独立副本，避免锁定被更新的 EXE")
	}
	dir := filepath.Join(filepath.Dir(target), ".easyrun-update")
	statePath := filepath.Join(dir, "state.json")
	// Restore an interrupted replacement before starting a server. Never trust
	// arbitrary paths from a state file, or apply an unverified leftover binary.
	if state, e := readState(statePath); e == nil && statePathsValid(state, target, dir) && (state.Phase == "installing" || state.Phase == "verifying") {
		if current, _ := fileHash(target); current != state.BeforeSHA {
			if err := restoreBinary(state); err != nil {
				return err
			}
		}
		state.Phase, state.Message = "rolled_back", "更新中断，已恢复原版本"
		state.UpdatedAt = time.Now().UTC()
		if err := writeJSONAtomic(statePath, state); err != nil {
			return err
		}
	}
	var cmd *exec.Cmd
	var done <-chan error
	if state, e := readState(statePath); e == nil && state.Phase == "prepared" && statePathsValid(state, target, dir) {
		kick := make(chan error, 1)
		kick <- nil
		close(kick)
		done = kick
	} else {
		cmd, done, _, err = startServer(target)
		if err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			stopChild(cmd, done)
			return nil
		case childErr := <-done:
			state, err := readState(statePath)
			if err != nil || state.Phase != "prepared" {
				return childErr
			}
			if !statePathsValid(state, target, dir) {
				return errors.New("更新状态文件路径不正确")
			}
			state.Phase, state.Message = "installing", "正在替换已校验的程序"
			state.UpdatedAt = time.Now().UTC()
			if err := writeJSONAtomic(statePath, state); err != nil {
				return err
			}
			installErr := replaceBinary(state)
			if installErr == nil {
				state.Phase, state.Message = "verifying", "正在检查新版本启动状态"
				_ = writeJSONAtomic(statePath, state)
				var boot string
				cmd, done, boot, installErr = startServer(target)
				if installErr == nil {
					probe, cancel := context.WithTimeout(ctx, 60*time.Second)
					installErr = verifyHealth(probe, address, boot, state.After, done)
					cancel()
				}
			}
			if installErr == nil {
				state.Phase, state.Message = "succeeded", "平台更新完成，健康检查已通过"
				state.UpdatedAt = time.Now().UTC()
				if err := writeJSONAtomic(statePath, state); err != nil {
					installErr = fmt.Errorf("记录更新验收结果失败：%w", err)
				} else {
					continue
				}
			}
			stopChild(cmd, done)
			current, _ := fileHash(state.Target)
			if current != state.BeforeSHA {
				if err := restoreBinary(state); err != nil {
					return fmt.Errorf("更新失败且回滚未完成：%w", err)
				}
			}
			state.Phase, state.Message = "rolled_back", "新版本未通过验收，已自动恢复原版本："+installErr.Error()
			state.UpdatedAt = time.Now().UTC()
			if err := writeJSONAtomic(statePath, state); err != nil {
				return err
			}
			cmd, done, _, err = startServer(target)
			if err != nil {
				return err
			}
		}
	}
}
