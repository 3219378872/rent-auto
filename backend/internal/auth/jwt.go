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
)

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
	if time.Now().Unix() >= c.ExpiresAt {
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
