package securestore

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Vault encrypts account passwords and session tokens before they reach the database.
type Vault struct {
	aead cipher.AEAD
}

func New(encodedKey string) (*Vault, error) {
	encodedKey = strings.TrimSpace(encodedKey)
	if encodedKey == "" {
		return nil, errors.New("ACCOUNT_MASTER_KEY is required")
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("decode ACCOUNT_MASTER_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("ACCOUNT_MASTER_KEY must decode to 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential AEAD: %w", err)
	}
	return &Vault{aead: aead}, nil
}

func (v *Vault) Encrypt(plaintext []byte, associatedData string) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate credential nonce: %w", err)
	}
	result := make([]byte, 1, 1+len(nonce)+len(plaintext)+v.aead.Overhead())
	result[0] = 1
	result = append(result, nonce...)
	result = v.aead.Seal(result, nonce, plaintext, []byte(associatedData))
	return result, nil
}

func (v *Vault) Decrypt(ciphertext []byte, associatedData string) ([]byte, error) {
	if len(ciphertext) == 0 {
		return nil, nil
	}
	if ciphertext[0] != 1 || len(ciphertext) < 1+v.aead.NonceSize()+v.aead.Overhead() {
		return nil, errors.New("credential ciphertext has an unsupported format")
	}
	nonce := ciphertext[1 : 1+v.aead.NonceSize()]
	payload := ciphertext[1+v.aead.NonceSize():]
	plaintext, err := v.aead.Open(nil, nonce, payload, []byte(associatedData))
	if err != nil {
		return nil, errors.New("credential ciphertext authentication failed")
	}
	return plaintext, nil
}
