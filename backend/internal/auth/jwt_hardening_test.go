package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// craftToken mints a token with full control over header/claims so Verify's
// hardening checks can be exercised in isolation.
func craftToken(t *testing.T, j *JWT, header string, c Claims) string {
	t.Helper()
	hb := base64.RawURLEncoding.EncodeToString([]byte(header))
	pb, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	pbes := base64.RawURLEncoding.EncodeToString(pb)
	body := hb + "." + pbes
	return body + "." + base64.RawURLEncoding.EncodeToString(j.mac([]byte(body)))
}

func validClaims() Claims {
	now := time.Now().Unix()
	return Claims{Sub: "admin", Ver: 0, IssuedAt: now, ExpiresAt: now + 3600}
}

// A correctly MACed token with alg=none must still be rejected: the header
// allowlist runs before (and independently of) the signature check.
func TestVerifyRejectsNoneAlg(t *testing.T) {
	j := NewJWT([]byte(testSecret))
	tok := craftToken(t, j, `{"alg":"none","typ":"JWT"}`, validClaims())
	if _, err := j.Verify(tok); err != ErrInvalidToken {
		t.Fatalf("none-alg token: want ErrInvalidToken, got %v", err)
	}
}

func TestVerifyRejectsForeignAlg(t *testing.T) {
	j := NewJWT([]byte(testSecret))
	tok := craftToken(t, j, `{"alg":"RS256","typ":"JWT"}`, validClaims())
	if _, err := j.Verify(tok); err != ErrInvalidToken {
		t.Fatalf("RS256-alg token: want ErrInvalidToken, got %v", err)
	}
}

func TestVerifyRejectsEmptySub(t *testing.T) {
	j := NewJWT([]byte(testSecret))
	c := validClaims()
	c.Sub = ""
	if _, err := j.Verify(craftToken(t, j, `{"alg":"HS256","typ":"JWT"}`, c)); err != ErrInvalidToken {
		t.Fatalf("empty sub: want ErrInvalidToken, got %v", err)
	}
}

func TestVerifyRejectsNegativeVer(t *testing.T) {
	j := NewJWT([]byte(testSecret))
	c := validClaims()
	c.Ver = -1
	if _, err := j.Verify(craftToken(t, j, `{"alg":"HS256","typ":"JWT"}`, c)); err != ErrInvalidToken {
		t.Fatalf("negative ver: want ErrInvalidToken, got %v", err)
	}
}

func TestVerifyRejectsFutureIAT(t *testing.T) {
	j := NewJWT([]byte(testSecret))
	c := validClaims()
	c.IssuedAt = time.Now().Unix() + 3600
	if _, err := j.Verify(craftToken(t, j, `{"alg":"HS256","typ":"JWT"}`, c)); err != ErrInvalidToken {
		t.Fatalf("future iat: want ErrInvalidToken, got %v", err)
	}
}

// Small clock skew stays accepted (60s drift allowance).
func TestVerifyAcceptsSlightFutureIAT(t *testing.T) {
	j := NewJWT([]byte(testSecret))
	c := validClaims()
	c.IssuedAt = time.Now().Unix() + 30
	if _, err := j.Verify(craftToken(t, j, `{"alg":"HS256","typ":"JWT"}`, c)); err != nil {
		t.Fatalf("30s-skewed iat must verify, got %v", err)
	}
}

// The fail-closed sentinel must survive wrapping so the auth middleware
// (server.go lane) can errors.Is-match store outages into 401s.
func TestFailClosedError(t *testing.T) {
	if !errors.Is(FailClosedError(nil), ErrStoreUnavailable) {
		t.Fatal("nil cause must still match ErrStoreUnavailable")
	}
	cause := errors.New(" dial tcp: connection refused ")
	if !errors.Is(FailClosedError(cause), ErrStoreUnavailable) {
		t.Fatal("wrapped store error must match ErrStoreUnavailable")
	}
}
