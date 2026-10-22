package portal

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/big"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"golang.org/x/crypto/sha3"
)

const MainnetUSDT = "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func base58Encode(data []byte) string {
	n := new(big.Int).SetBytes(data)
	base := big.NewInt(58)
	var out []byte
	for n.Sign() > 0 {
		q, r := new(big.Int), new(big.Int)
		q.QuoRem(n, base, r)
		out = append(out, alphabet[r.Int64()])
		n = q
	}
	for _, b := range data {
		if b != 0 {
			break
		}
		out = append(out, '1')
	}
	for a, b := 0, len(out)-1; a < b; a, b = a+1, b-1 {
		out[a], out[b] = out[b], out[a]
	}
	return string(out)
}
func addressBytes(address string) ([]byte, error) {
	if len(address) != 34 {
		return nil, errors.New("invalid TRON address")
	}
	n := new(big.Int)
	for _, c := range []byte(address) {
		v := bytes.IndexByte([]byte(alphabet), c)
		if v < 0 {
			return nil, errors.New("invalid base58")
		}
		n.Mul(n, big.NewInt(58))
		n.Add(n, big.NewInt(int64(v)))
	}
	raw := n.Bytes()
	if len(raw) != 25 || raw[0] != 0x41 {
		return nil, errors.New("invalid TRON address version")
	}
	a := sha256.Sum256(raw[:21])
	b := sha256.Sum256(a[:])
	if !bytes.Equal(raw[21:], b[:4]) {
		return nil, errors.New("invalid TRON checksum")
	}
	return raw[:21], nil
}
func ValidAddress(s string) bool { _, e := addressBytes(s); return e == nil }
func publicAddress(key *secp.PrivateKey) string {
	pub := key.PubKey().SerializeUncompressed()
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write(pub[1:])
	raw := append([]byte{0x41}, h.Sum(nil)[12:]...)
	a := sha256.Sum256(raw)
	b := sha256.Sum256(a[:])
	return base58Encode(append(raw, b[:4]...))
}
func deriveKey(seed []byte, index uint32) (*secp.PrivateKey, error) {
	if len(seed) < 16 || len(seed) > 64 || index >= 1<<31 {
		return nil, errors.New("invalid HD seed or derivation index")
	}
	mac := hmac.New(sha512.New, []byte("Bitcoin seed"))
	_, _ = mac.Write(seed)
	sum := mac.Sum(nil)
	var key secp.ModNScalar
	if key.SetByteSlice(sum[:32]) || key.IsZero() {
		return nil, errors.New("invalid BIP32 master key")
	}
	chain := append([]byte{}, sum[32:]...)
	for _, child := range []uint32{44 | 1<<31, 195 | 1<<31, 0 | 1<<31, 0, index} {
		priv := secp.NewPrivateKey(&key)
		var data []byte
		if child >= 1<<31 {
			data = append([]byte{0}, priv.Serialize()...)
		} else {
			data = priv.PubKey().SerializeCompressed()
		}
		priv.Zero()
		var number [4]byte
		binary.BigEndian.PutUint32(number[:], child)
		data = append(data, number[:]...)
		mac = hmac.New(sha512.New, chain)
		_, _ = mac.Write(data)
		sum = mac.Sum(nil)
		var tweak secp.ModNScalar
		if tweak.SetByteSlice(sum[:32]) || tweak.IsZero() {
			key.Zero()
			return nil, errors.New("invalid BIP32 child; allocate a new index")
		}
		key.Add(&tweak)
		tweak.Zero()
		if key.IsZero() {
			return nil, errors.New("invalid BIP32 child; allocate a new index")
		}
		copy(chain, sum[32:])
	}
	out := secp.NewPrivateKey(&key)
	key.Zero()
	return out, nil
}
func decodeSeed(s string) ([]byte, error) { return hex.DecodeString(s) }
