package auth

import (
	"testing"
	"time"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestJWTRoundTrip(t *testing.T) {
	j := NewJWT([]byte(testSecret))
	tok, exp, err := j.Sign("admin", 0, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.After(time.Now()) {
		t.Fatal("exp should be in the future")
	}
	c, err := j.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.Sub != "admin" {
		t.Fatalf("sub = %q", c.Sub)
	}
}

func TestJWTExpired(t *testing.T) {
	j := NewJWT([]byte(testSecret))
	tok, _, _ := j.Sign("admin", 0, -time.Second)
	if _, err := j.Verify(tok); err != ErrExpiredToken {
		t.Fatalf("want ErrExpiredToken, got %v", err)
	}
}

func TestJWTTampered(t *testing.T) {
	j := NewJWT([]byte(testSecret))
	tok, _, _ := j.Sign("admin", 0, time.Minute)
	if _, err := j.Verify(tok + "x"); err != ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
	other := NewJWT([]byte("ffffffffffffffffffffffffffffffff"))
	tok2, _, _ := other.Sign("admin", 0, time.Minute)
	if _, err := j.Verify(tok2); err != ErrInvalidToken {
		t.Fatalf("cross-secret verify must fail, got %v", err)
	}
}

func TestJWTGarbage(t *testing.T) {
	j := NewJWT([]byte(testSecret))
	for _, tok := range []string{"", "a.b.c", "x.y"} {
		if _, err := j.Verify(tok); err != ErrInvalidToken {
			t.Fatalf("token %q: want ErrInvalidToken, got %v", tok, err)
		}
	}
}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "s3cret") {
		t.Fatal("correct password rejected")
	}
	if CheckPassword(h, "wrong") {
		t.Fatal("wrong password accepted")
	}
}
