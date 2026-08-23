package secrets

import (
	"bytes"
	"encoding/hex"
	"testing"
)

const testKey = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"

func mustBox(t *testing.T) *Box {
	t.Helper()
	key, err := hex.DecodeString(testKey)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := mustBox(t)
	enc, err := b.Seal([]byte("uu_token_secret_value"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := b.Open(enc)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "uu_token_secret_value" {
		t.Fatalf("round trip mismatch: %q", plain)
	}
}

func TestSealNonDeterministic(t *testing.T) {
	b := mustBox(t)
	e1, _ := b.Seal([]byte("same"))
	e2, _ := b.Seal([]byte("same"))
	if e1 == e2 {
		t.Fatal("nonce reuse: ciphertexts identical")
	}
}

func TestTamperedCiphertext(t *testing.T) {
	b := mustBox(t)
	enc, _ := b.Seal([]byte("data"))
	raw := []byte(enc)
	raw[len(raw)/2] ^= 0xFF
	if _, err := b.Open(string(raw)); err == nil {
		t.Fatal("tampered ciphertext must not open")
	}
}

func TestNoMasterKey(t *testing.T) {
	if _, err := NewBox(nil); err != ErrNoMasterKey {
		t.Fatalf("want ErrNoMasterKey, got %v", err)
	}
	var buf bytes.Buffer
	_ = buf
}
