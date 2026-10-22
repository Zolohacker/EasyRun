package securestore

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestVaultRoundTripAndBinding(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	vault, err := New(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := vault.Encrypt([]byte("secret"), "acct_1:password")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := vault.Decrypt(ciphertext, "acct_1:password")
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "secret" {
		t.Fatalf("unexpected plaintext %q", plaintext)
	}
	if _, err := vault.Decrypt(ciphertext, "acct_2:password"); err == nil {
		t.Fatal("ciphertext must be bound to its account")
	}
}
