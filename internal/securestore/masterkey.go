package securestore

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadMasterKey preserves an existing key, imports a legacy deployment key or
// creates exactly one 256-bit key. Only complete, fsynced files are published;
// linking without replacement handles concurrent first starts atomically.
func LoadMasterKey(dataDir, legacy string, encryptedDataExists func() (bool, error)) (string, string, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, "account-master.key")
	legacy = strings.TrimSpace(legacy)
	if legacy != "" {
		if _, err := New(legacy); err != nil {
			return "", path, err
		}
	}
	read := func() (string, error) {
		info, e := os.Lstat(path)
		if e != nil {
			return "", e
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("主密钥路径必须是普通文件")
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return "", e
		}
		if len(raw) > 128 {
			return "", errors.New("主密钥文件格式不正确")
		}
		key := strings.TrimSpace(string(raw))
		if _, e = New(key); e != nil {
			return "", errors.New("主密钥文件格式不正确，请恢复原文件")
		}
		if legacy != "" && subtle.ConstantTimeCompare([]byte(key), []byte(legacy)) != 1 {
			return "", errors.New("原环境主密钥与持久化文件不一致，请核对原部署备份")
		}
		if e = protectSecretFile(path); e != nil {
			return "", fmt.Errorf("保护主密钥文件：%w", e)
		}
		return key, nil
	}
	if key, e := read(); e == nil {
		return key, path, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", path, e
	}
	if legacy == "" && encryptedDataExists != nil {
		exists, e := encryptedDataExists()
		if e != nil {
			return "", path, e
		}
		if exists {
			return "", path, errors.New("数据库已有加密数据但主密钥文件缺失，请恢复 data/account-master.key 或原部署主密钥")
		}
	}
	key := legacy
	if key == "" {
		var b [32]byte
		if _, err = rand.Read(b[:]); err != nil {
			return "", path, err
		}
		key = base64.StdEncoding.EncodeToString(b[:])
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", path, err
	}
	f, err := os.CreateTemp(dir, ".master-key-")
	if err != nil {
		return "", path, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = protectSecretFile(tmp); err != nil {
		_ = f.Close()
		return "", path, err
	}
	if _, err = f.WriteString(key + "\n"); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", path, err
	}
	if err = os.Link(tmp, path); err != nil && !errors.Is(err, os.ErrExist) {
		return "", path, fmt.Errorf("持久化主密钥：%w", err)
	}
	// Best-effort directory fsync; Windows does not support syncing directories.
	if d, e := os.Open(dir); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	stored, err := read()
	return stored, path, err
}
