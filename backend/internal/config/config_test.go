package config

import (
	"os"
	"testing"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		os.Setenv(k, v)
	}
	t.Cleanup(func() {
		for k := range kv {
			os.Unsetenv(k)
		}
	})
}

func TestLoadValid(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://x/y",
		"JWT_SECRET":   "0123456789abcdef0123456789abcdef",
	})
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.DryRunDefault {
		t.Fatal("dry run default should be true")
	}
	if c.Addr != ":8080" {
		t.Fatalf("addr = %q", c.Addr)
	}
}

func TestLoadMasterKey(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":   "postgres://x/y",
		"JWT_SECRET":     "0123456789abcdef0123456789abcdef",
		"APP_MASTER_KEY": "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
	})
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.MasterKey) != 32 {
		t.Fatalf("master key len = %d", len(c.MasterKey))
	}
}

func TestLoadErrors(t *testing.T) {
	setEnv(t, map[string]string{"JWT_SECRET": "0123456789abcdef0123456789abcdef"})
	if _, err := Load(); err == nil {
		t.Fatal("missing DATABASE_URL must fail")
	}
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x/y", "JWT_SECRET": "short"})
	if _, err := Load(); err == nil {
		t.Fatal("short JWT_SECRET must fail")
	}
	setEnv(t, map[string]string{
		"DATABASE_URL":   "postgres://x/y",
		"JWT_SECRET":     "0123456789abcdef0123456789abcdef",
		"APP_MASTER_KEY": "zzzz",
	})
	if _, err := Load(); err == nil {
		t.Fatal("bad APP_MASTER_KEY must fail")
	}
}
