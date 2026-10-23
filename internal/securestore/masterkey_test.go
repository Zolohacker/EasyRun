package securestore

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMasterKeyGeneratedOnceAcrossConcurrentStarts(t *testing.T) {
	dir := t.TempDir()
	keys := make(chan string, 24)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k, _, err := LoadMasterKey(dir, "", nil)
			if err != nil {
				t.Error(err)
				return
			}
			keys <- k
		}()
	}
	wg.Wait()
	close(keys)
	first := ""
	for key := range keys {
		if first == "" {
			first = key
		}
		if key != first {
			t.Fatal("concurrent startups chose different keys")
		}
	}
	data, err := base64.StdEncoding.DecodeString(first)
	if err != nil || len(data) != 32 {
		t.Fatal("invalid generated master key")
	}
	again, path, err := LoadMasterKey(dir, "", nil)
	if err != nil || again != first {
		t.Fatal("restart rotated key")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(bytes.TrimSpace(raw)) != first {
		t.Fatal("persisted key differs")
	}
	other, _, err := LoadMasterKey(t.TempDir(), "", nil)
	if err != nil || other == first {
		t.Fatal("independent deployments share a key")
	}
}
func TestLegacyMasterKeyMigratesWithoutChangingEncryptedData(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	dir := t.TempDir()
	v, _ := New(key)
	cipher, _ := v.Encrypt([]byte("preserve-wallet"), "fixture")
	saved, _, err := LoadMasterKey(dir, key, func() (bool, error) { return true, nil })
	if err != nil || saved != key {
		t.Fatal("legacy key changed")
	}
	restarted, _, err := LoadMasterKey(dir, "", func() (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := New(restarted)
	plain, err := v2.Decrypt(cipher, "fixture")
	if err != nil || string(plain) != "preserve-wallet" {
		t.Fatal("existing ciphertext lost")
	}
	wrong := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	if _, _, err := LoadMasterKey(dir, wrong, nil); err == nil {
		t.Fatal("conflicting key accepted")
	}
}
func TestMasterKeyLossAndCorruptionNeverRotateSilently(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := LoadMasterKey(dir, "", func() (bool, error) { return true, nil }); err == nil {
		t.Fatal("new key created over encrypted deployment")
	}
	if _, err := os.Stat(filepath.Join(dir, "account-master.key")); !os.IsNotExist(err) {
		t.Fatal("unexpected key file")
	}
	if err := os.WriteFile(filepath.Join(dir, "account-master.key"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadMasterKey(dir, "", nil); err == nil {
		t.Fatal("corrupt file replaced")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "account-master.key"))
	if string(raw) != "corrupt" {
		t.Fatal("corrupt file changed")
	}
}
