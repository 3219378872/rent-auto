// Package auth implements HS256 JWT sessions and bcrypt password handling.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidToken = errors.New("auth: invalid token")
	ErrExpiredToken = errors.New("auth: token expired")
	// ErrStoreUnavailable marks session-epoch store read failures. The
	// requireAuth epoch comparison (server.go, owned by a parallel lane)
	// must treat this as fail-closed 401 — a nil/unreadable store must
	// never fall back to ver=0 and skip revocation. Use FailClosedError
	// to wrap the underlying store error.
	ErrStoreUnavailable = errors.New("auth: credential store unavailable")
)

// FailClosedError wraps a session-epoch store read failure so the auth
// middleware can distinguish fail-closed 401s from ordinary invalid tokens.
func FailClosedError(err error) error {
	if err == nil {
		return ErrStoreUnavailable
	}
	return fmt.Errorf("%w: %v", ErrStoreUnavailable, err)
}

type Claims struct {
	Sub       string `json:"sub"`
	Ver       int64  `json:"ver"` // session epoch; mismatch = revoked server-side
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

type JWT struct {
	secret []byte
}

func NewJWT(secret []byte) *JWT { return &JWT{secret: secret} }

// Sign issues a token bound to the given session epoch. Bumping the epoch
// server-side (logout) instantly invalidates every earlier token.
func (j *JWT) Sign(sub string, ver int64, ttl time.Duration) (token string, exp time.Time, err error) {
	now := time.Now()
	exp = now.Add(ttl)
	c := Claims{Sub: sub, Ver: ver, IssuedAt: now.Unix(), ExpiresAt: exp.Unix()}
	header := base64URL([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(c)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign: %w", err)
	}
	body := header + "." + base64URL(payload)
	return body + "." + base64URL(j.mac([]byte(body))), exp, nil
}

func (j *JWT) Verify(token string) (*Claims, error) {
	parts := splitToken(token)
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	hb, pb, sb := parts[0], parts[1], parts[2]
	// Explicit header check: only HS256 we minted is accepted. A crafted
	// "alg":"none" (or any other alg) header must be rejected before the
	// signature comparison, not after.
	rawHeader, err := base64.RawURLEncoding.DecodeString(hb)
	if err != nil {
		return nil, ErrInvalidToken
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(rawHeader, &hdr); err != nil || hdr.Alg != "HS256" {
		return nil, ErrInvalidToken
	}
	want := j.mac([]byte(hb + "." + pb))
	got, err := base64.RawURLEncoding.DecodeString(sb)
	if err != nil || !hmac.Equal(want, got) {
		return nil, ErrInvalidToken
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(pb)
	if err != nil {
		return nil, ErrInvalidToken
	}
	var c Claims
	if err := json.Unmarshal(rawPayload, &c); err != nil {
		return nil, ErrInvalidToken
	}
	now := time.Now().Unix()
	if c.Sub == "" || c.Ver < 0 {
		return nil, ErrInvalidToken
	}
	// 60s future-issue skew allowance for clock drift; anything beyond is
	// a forged or replayed token.
	if c.IssuedAt > now+60 {
		return nil, ErrInvalidToken
	}
	if now >= c.ExpiresAt {
		return nil, ErrExpiredToken
	}
	return &c, nil
}

func (j *JWT) mac(data []byte) []byte {
	h := hmac.New(sha256.New, j.secret)
	h.Write(data)
	return h.Sum(nil)
}

func base64URL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func splitToken(t string) []string {
	out := make([]string, 0, 3)
	start := 0
	for i := 0; i < len(t); i++ {
		if t[i] == '.' {
			out = append(out, t[start:i])
			start = i + 1
		}
	}
	out = append(out, t[start:])
	return out
}
