package uu

import (
	"bytes"
	_ "embed"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// Fixtures generated with openssl enc -aes-128-ecb (see testing-strategy.md):
// cross-implementation vectors prove byte-level AES-ECB/PKCS7 compatibility.
var (
	fixKey1   = mustHex("testdata/key.hex")
	fixPlain1 = mustFile("testdata/p1.txt")
	fixEnc1   = strings.TrimSpace(mustFile("testdata/p1.b64"))
)

func mustHex(p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		panic(err)
	}
	k, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		panic(err)
	}
	return k
}

func mustFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestAESECBMatchesOpenSSLVector(t *testing.T) {
	c, err := NewCryptWithKey(fixKey1)
	if err != nil {
		t.Fatal(err)
	}
	got := c.EncryptAES([]byte(fixPlain1))
	if got != fixEnc1 {
		t.Fatalf("ciphertext mismatch:\n want=%s\n got =%s", fixEnc1, got)
	}
	back, err := c.DecryptAES(got)
	if err != nil || !bytes.Equal(back, []byte(fixPlain1)) {
		t.Fatalf("decrypt round trip failed: %v %q", err, back)
	}
}

func TestDecryptOpenSSLVector(t *testing.T) {
	c, err := NewCryptWithKey(fixKey1)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.DecryptAES(fixEnc1)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != fixPlain1 {
		t.Fatalf("want %q got %q", fixPlain1, plain)
	}
}

func TestPKCS7EdgeCases(t *testing.T) {
	if _, err := pkcs7Unpad([]byte{0x05, 0x02}, 16); err == nil {
		t.Fatal("invalid padding must error")
	}
	padded := pkcs7Pad([]byte("1234567890123456"), 16) // full block pad
	if len(padded) != 32 {
		t.Fatalf("pad len = %d", len(padded))
	}
	got, err := pkcs7Unpad(padded, 16)
	if err != nil || string(got) != "1234567890123456" {
		t.Fatalf("full-block pad roundtrip: %v %q", err, got)
	}
}

func TestBadAESKeyLength(t *testing.T) {
	if _, err := NewCryptWithKey([]byte("short")); err == nil {
		t.Fatal("short key must be rejected")
	}
}

func TestRandomStringAlphabet(t *testing.T) {
	s := RandomString(65)
	if len(s) != 65 {
		t.Fatal("length")
	}
	for _, r := range s {
		if !strings.ContainsRune(alphabet, r) {
			t.Fatalf("bad rune %q", r)
		}
	}
}
