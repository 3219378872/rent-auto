// Package uu implements the youpin898 (悠悠有品) API client.
//
// Behavioral spec: docs/knowledge/design/platform-uu-api-notes.md
// Reference implementation (read-only): upstream Steamauto uuyoupinapi.
package uu

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	mathrand "math/rand"
)

// uuPublicKeyPEM is the platform RSA public key embedded in the official client
// (identical to the one used by upstream Steamauto).
const uuPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAv9BDdhCDahZNFuJeesx3gzoQfD7pE0AeWiNBZlc21ph6kU9zd58X/1warV3C1VIX0vMAmhOcj5u86i+L2Lb2V68dX2Nb70MIDeW6Ibe8d0nF8D30tPsM7kaAyvxkY6ECM6RHGNhV4RrzkHmf5DeR9bybQGE0A9jcjuxszD1wsW/n19eeom7MroHqlRorp5LLNR8bSbmhTw6M/RQ/Fm3lKjKcvs1QNVyBNimrbD+ZVPE/KHSZLQ1jdF6tppvFnGxgJU9NFmxGFU0hx6cZiQHkhOQfGDFkElxgtj8gFJ1narTwYbvfe5nGSiznv/EUJSjTHxzX1TEkex0+5j4vSANt1QIDAQAB
-----END PUBLIC KEY-----`

var errCrypt = errors.New("uu: crypto error")

// Crypt mirrors the platform envelope scheme: random 16-byte AES key,
// AES-128-ECB + PKCS7 for the payload, RSA-PKCS1v15 for the key.
type Crypt struct {
	aesKey    []byte
	publicKey *rsa.PublicKey
}

func NewCrypt() (*Crypt, error) {
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("uu: aes key: %w", err)
	}
	return NewCryptWithKey(key)
}

// NewCryptWithKey allows deterministic construction for tests/fixtures.
func NewCryptWithKey(aesKey []byte) (*Crypt, error) {
	if len(aesKey) != 16 {
		return nil, fmt.Errorf("%w: aes key must be 16 bytes", errCrypt)
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errCrypt, err)
	}
	_ = block
	pub, err := parsePublicKey()
	if err != nil {
		return nil, err
	}
	return &Crypt{aesKey: aesKey, publicKey: pub}, nil
}

func parsePublicKey() (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(uuPublicKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("%w: bad public key pem", errCrypt)
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errCrypt, err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: not an RSA key", errCrypt)
	}
	return rsaPub, nil
}

func (c *Crypt) EncryptAES(plain []byte) string {
	padded := pkcs7Pad(plain, aes.BlockSize)
	out := make([]byte, len(padded))
	block, _ := aes.NewCipher(c.aesKey)
	for off := 0; off < len(padded); off += aes.BlockSize {
		block.Encrypt(out[off:off+aes.BlockSize], padded[off:off+aes.BlockSize])
	}
	return base64.StdEncoding.EncodeToString(out)
}

func (c *Crypt) DecryptAES(encB64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encB64)
	if err != nil || len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%w: bad ciphertext", errCrypt)
	}
	out := make([]byte, len(raw))
	block, _ := aes.NewCipher(c.aesKey)
	for off := 0; off < len(raw); off += aes.BlockSize {
		block.Decrypt(out[off:off+aes.BlockSize], raw[off:off+aes.BlockSize])
	}
	unpadded, err := pkcs7Unpad(out, aes.BlockSize)
	if err != nil {
		return nil, err
	}
	return unpadded, nil
}

func (c *Crypt) EncryptedAESKey() (string, error) {
	enc, err := rsa.EncryptPKCS1v15(rand.Reader, c.publicKey, c.aesKey) //nolint:staticcheck // 平台线协议强制 PKCS#1 v1.5，服务端固定无法换 OAEP
	if err != nil {
		return "", fmt.Errorf("%w: rsa encrypt: %v", errCrypt, err)
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

// Fingerprint returns a short hex digest of the AES key (for logs; never the key itself).
func (c *Crypt) Fingerprint() string {
	sum := sha256.Sum256(c.aesKey)
	return hex.EncodeToString(sum[:4])
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("%w: invalid padding size", errCrypt)
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, fmt.Errorf("%w: invalid padding byte", errCrypt)
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, fmt.Errorf("%w: inconsistent padding", errCrypt)
		}
	}
	return data[:len(data)-pad], nil
}

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandomString mirrors upstream generate_random_string (A-Za-z0-9).
func RandomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[mathrand.Intn(len(alphabet))]
	}
	return string(b)
}

// UUID4 renders a canonical UUID v4 (8-4-4-4-12 with dashes, version nibble 4,
// RFC 4122 variant) from crypto/rand. The deviceW2 handshake rejects payloads
// whose iud is not in this exact shape (observed 2026-08-27: 36 random
// alphanumerics yield a silent empty 200).
func UUID4() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
