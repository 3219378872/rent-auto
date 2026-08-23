// Package secrets provides AES-256-GCM encryption for credential values at rest.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

var ErrNoMasterKey = errors.New("secrets: master key not configured")

type Box struct {
	aead cipher.AEAD
}

func NewBox(masterKey []byte) (*Box, error) {
	if len(masterKey) == 0 {
		return nil, ErrNoMasterKey
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: %w", err)
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plain []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("seals: %w", err)
	}
	out := b.aead.Seal(nonce, nonce, plain, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (b *Box) Open(enc string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns+1 {
		return nil, errors.New("open: ciphertext too short")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	return plain, nil
}
