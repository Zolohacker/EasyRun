package protocol

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	AppVersion   = "8.7.9"
	BuildVersion = "26101910"
	AppCode      = "SD001"
	Platform     = "2"
	Channel      = "xiaomi"
	fieldSeed    = "F44B0282BEA83557"
	sliderSeed   = "Ukp3hmSe7BmMcgbE"
	aesIV        = "01234ABCDEF56789"
)

func environmentSignSeed(environment int) string {
	if environment&^2 == 9 {
		return "Z3MDTC9JATA1SSYB"
	}
	return "09L8MJRMJKXW8TPF"
}

func environmentRunSecret(environment int) string {
	if environment == 1 || environment == 10 {
		return "STnuKS6lwrCo4OSSJup7sEMZ5SZYMNcq"
	}
	if environment&^2 == 9 {
		return "9dLCADYy64tKQXoHYTGfrs4VlY0Yg4sI"
	}
	return "QCxhZB6oagpOaQ2jitHucZd8bX34ZEVp"
}

func deriveAES256Key(seed string) []byte {
	key := make([]byte, 32)
	copy(key, []byte(seed))
	return key
}

func pkcs7Pad(input []byte, blockSize int) []byte {
	padding := blockSize - len(input)%blockSize
	return append(input, bytes.Repeat([]byte{byte(padding)}, padding)...)
}

func pkcs7Unpad(input []byte, blockSize int) ([]byte, error) {
	if len(input) == 0 || len(input)%blockSize != 0 {
		return nil, errors.New("invalid PKCS#7 payload length")
	}
	padding := int(input[len(input)-1])
	if padding == 0 || padding > blockSize || padding > len(input) {
		return nil, errors.New("invalid PKCS#7 padding")
	}
	for _, value := range input[len(input)-padding:] {
		if int(value) != padding {
			return nil, errors.New("invalid PKCS#7 padding")
		}
	}
	return input[:len(input)-padding], nil
}

func aesCBCBase64(plaintext []byte, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(plaintext, block.BlockSize())
	output := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(aesIV)).CryptBlocks(output, padded)
	return base64.StdEncoding.EncodeToString(output), nil
}

func aesCBCBase64Decrypt(encoded string, key []byte) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(ciphertext) == 0 || len(ciphertext)%block.BlockSize() != 0 {
		return "", errors.New("invalid AES-CBC ciphertext length")
	}
	plaintext := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, []byte(aesIV)).CryptBlocks(plaintext, ciphertext)
	plaintext, err = pkcs7Unpad(plaintext, block.BlockSize())
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// EncryptField reproduces the 8.7.9 FormBody sensitive-field transform.
func EncryptField(value string) (string, error) {
	return aesCBCBase64([]byte(value), deriveAES256Key(fieldSeed))
}

// DecryptField reverses the response-side sensitive-field transform used for
// userName, schoolName and studentNumber in the 8.7.9 protocol.
func DecryptField(value string) (string, error) {
	return aesCBCBase64Decrypt(value, deriveAES256Key(fieldSeed))
}

// SignBody signs the exact compact JSON bytes sent on the wire.
func SignBody(body []byte, environment int) (string, error) {
	digest := sha256.Sum256(body)
	hexDigest := hex.EncodeToString(digest[:])
	rotated := hexDigest[len(hexDigest)-8:] + hexDigest[8:len(hexDigest)-8] + hexDigest[:8]
	return aesCBCBase64([]byte(rotated), deriveAES256Key(environmentSignSeed(environment)))
}

func EncryptSliderPoint(rawX float64) (string, error) {
	point := struct {
		X float64 `json:"x"`
		Y int     `json:"y"`
	}{X: float64(int(rawX-6.5)) + 16.5, Y: 15}
	raw, err := json.Marshal(point)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher([]byte(sliderSeed))
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(raw, block.BlockSize())
	output := make([]byte, len(padded))
	for offset := 0; offset < len(padded); offset += block.BlockSize() {
		block.Encrypt(output[offset:offset+block.BlockSize()], padded[offset:offset+block.BlockSize()])
	}
	return base64.StdEncoding.EncodeToString(output), nil
}

func RunImageRecord(runRecordCode, appCode, appVersion, timestamp string, environment int) string {
	digest := md5.Sum([]byte(runRecordCode + appCode + appVersion + environmentRunSecret(environment) + timestamp))
	return hex.EncodeToString(digest[:])
}

func compactJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func decodeBase64Image(value string) ([]byte, error) {
	if comma := bytes.IndexByte([]byte(value), ','); comma >= 0 {
		value = value[comma+1:]
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode captcha image: %w", err)
	}
	if len(decoded) == 0 {
		return nil, errors.New("captcha image is empty")
	}
	return decoded, nil
}
