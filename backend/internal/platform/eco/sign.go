// Package eco implements the ECOSteam open-platform API client.
//
// Spec: docs/knowledge/design/platform-eco-api-notes.md (official docs digest).
package eco

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SignString builds the canonical pre-signature string:
// first-level params sorted case-insensitively; scalars rendered raw
// (strings unquoted), composites as compact JSON without key re-sorting;
// empty strings and nils omitted. Matches the official doc examples.
func SignString(params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		la, lb := strings.ToLower(a), strings.ToLower(b)
		if la == lb {
			return a < b
		}
		return la < lb
	})
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+canonicalValue(params[k]))
	}
	return strings.Join(parts, "&")
}

func canonicalValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return CompactJSON(t)
	}
}

// CompactJSON marshals without HTML escaping or extra whitespace,
// preserving struct field order (required for signature stability).
func CompactJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return strings.TrimRight(buf.String(), "\n")
}

// Sign produces the base64 SHA256withRSA signature over the canonical string.
func Sign(privateKeyPEM []byte, canonical string) (string, error) {
	key, err := ParsePrivateKey(privateKeyPEM)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(canonical))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("eco: sign: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifySignature checks a signature against the public half of the pair (tests).
func VerifySignature(publicKeyPEM []byte, canonical, sigB64 string) error {
	pub, err := ParsePublicKey(publicKeyPEM)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("eco: sig decode: %w", err)
	}
	digest := sha256.Sum256([]byte(canonical))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig)
}

// ParsePrivateKey accepts PKCS#8 or PKCS#1 DER inside PEM, raw base64 body too.
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := decodePEM(pemBytes)
	if block == nil {
		// try raw base64 body (docs recommend header-less keys)
		if der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(pemBytes))); err == nil {
			return parseKeyDER(der)
		}
		return nil, fmt.Errorf("eco: private key pem decode failed")
	}
	return parseKeyDER(block.Bytes)
}

func parseKeyDER(der []byte) (*rsa.PrivateKey, error) {
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("eco: private key not RSA")
		}
		return rk, nil
	}
	k, err := x509.ParsePKCS1PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("eco: parse private key: %w", err)
	}
	return k, nil
}

func ParsePublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := decodePEM(pemBytes)
	if block != nil {
		if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
			if rk, ok := pub.(*rsa.PublicKey); ok {
				return rk, nil
			}
		}
	}
	if der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(pemBytes))); err == nil {
		pub, err := x509.ParsePKIXPublicKey(der)
		if err == nil {
			if rk, ok := pub.(*rsa.PublicKey); ok {
				return rk, nil
			}
		}
	}
	return nil, fmt.Errorf("eco: parse public key failed")
}
