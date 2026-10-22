package portal

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	bip39 "github.com/cosmos/go-bip39"
)

const walletDerivationPath = "m/44'/195'/0'/0/index"

type walletBackup struct {
	Kind           string `json:"kind"`
	Mnemonic       string `json:"mnemonic,omitempty"`
	SeedHex        string `json:"seed_hex,omitempty"`
	Network        string `json:"network"`
	DerivationPath string `json:"derivation_path"`
	Passphrase     string `json:"passphrase"` // BIP39 additional passphrase is empty, not the administrator password.
}

func newMnemonicWallet() ([]byte, []byte, error) {
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return nil, nil, err
	}
	defer clear(entropy)
	phrase, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return nil, nil, err
	}
	seed, err := bip39.NewSeedWithErrorChecking(phrase, "")
	return seed, []byte(phrase), err
}

// The caller owns the settings row lock and commits seed + mnemonic atomically.
func (s *Service) sealNewMnemonicWallet(sealed map[string][]byte) error {
	if sealed == nil || len(sealed["seed"]) > 0 {
		return errors.New("钱包已存在或密钥存储格式不正确")
	}
	seed, phrase, err := newMnemonicWallet()
	if err != nil {
		return err
	}
	defer clear(seed)
	defer clear(phrase)
	// Keep the original seed slot's hexadecimal representation for old wallets.
	seedHex := []byte(hex.EncodeToString(seed))
	defer clear(seedHex)
	sealed["seed"], err = s.vault.Encrypt(seedHex, "portal-secret:seed")
	if err != nil {
		return err
	}
	sealed["mnemonic"], err = s.vault.Encrypt(phrase, "portal-secret:mnemonic")
	return err
}

func (s *Service) exportWalletBackup(w http.ResponseWriter, r *http.Request) {
	if !requireSuper(w, r) {
		return
	}
	if !s.allow("wallet-export:"+currentUser(r).ID, 3, 5*time.Minute) {
		fail(w, 429, "钱包导出较频繁，请五分钟后重试")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := decode(w, r, &input); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !s.passwordOK(r, input.Password) {
		fail(w, 403, "请重新输入当前超级管理员密码")
		return
	}
	// Read config and encrypted material from the same snapshot. Ordinary
	// settings reads never decrypt the mnemonic or return it to the browser.
	var configRaw, secretsRaw []byte
	if err := s.db.QueryRow(r.Context(), `SELECT config,secrets FROM portal_settings WHERE id=1`).Scan(&configRaw, &secretsRaw); err != nil {
		s.dbError(w, err)
		return
	}
	cfg, err := decodeSettingsConfig(configRaw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	var sealed map[string][]byte
	if err = json.Unmarshal(secretsRaw, &sealed); err != nil {
		s.dbError(w, err)
		return
	}
	if len(sealed["seed"]) == 0 {
		fail(w, 409, "请先创建收款钱包")
		return
	}
	seedHex, err := s.vault.Decrypt(sealed["seed"], "portal-secret:seed")
	if err != nil {
		s.dbError(w, err)
		return
	}
	defer clear(seedHex)
	seed, err := hex.DecodeString(string(seedHex))
	if err != nil || len(seed) < 32 || len(seed) > 64 {
		fail(w, 409, "钱包 seed 校验失败，请核对加密备份")
		return
	}
	defer clear(seed)
	backup := walletBackup{Kind: "legacy_seed", SeedHex: string(seedHex), Network: cfg.Wallet.Network, DerivationPath: walletDerivationPath}
	if len(sealed["mnemonic"]) > 0 {
		phrase, err := s.vault.Decrypt(sealed["mnemonic"], "portal-secret:mnemonic")
		if err != nil {
			s.dbError(w, err)
			return
		}
		defer clear(phrase)
		restored, err := bip39.NewSeedWithErrorChecking(string(phrase), "")
		defer clear(restored)
		if err != nil || subtle.ConstantTimeCompare(restored, seed) != 1 {
			fail(w, 409, "助记词与钱包 seed 校验不一致，请核对加密备份")
			return
		}
		backup.Kind, backup.Mnemonic, backup.SeedHex = "bip39", string(phrase), ""
	}
	s.audit(r.Context(), "wallet.backup-export", backup.Kind)
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	jsonReply(w, 200, backup)
}
