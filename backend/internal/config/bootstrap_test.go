package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadBootstrapPasswordRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if _, err := ReadBootstrapPassword(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	password, err := BootstrapPassword()
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteBootstrapPassword(path, password); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadBootstrapPassword(path); err != nil || got != password {
		t.Fatalf("recover: %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBootstrapPassword(path); err == nil {
		t.Fatal("read world-readable password")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBootstrapPassword(link); err == nil {
		t.Fatal("recovered from symlink")
	}
	for _, value := range []string{"short", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", password + password} {
		bad := filepath.Join(t.TempDir(), "bad")
		if err := WriteBootstrapPassword(bad, value); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadBootstrapPassword(bad); err == nil {
			t.Fatal("invalid bootstrap format accepted")
		}
	}
}

func TestBootstrapPasswordRestrictedExclusiveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "password")
	if err := WriteBootstrapPassword(path, "synthetic-test-password"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file mode: %v %v", info, err)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || dir.Mode().Perm() != 0700 {
		t.Fatalf("directory mode: %v %v", dir, err)
	}
	if err := WriteBootstrapPassword(path, "replacement"); err == nil {
		t.Fatal("existing file overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "synthetic-test-password\n" {
		t.Fatalf("content changed: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteBootstrapPassword(link, "replacement"); err == nil {
		t.Fatal("followed final symlink")
	}
	if err := WriteBootstrapPassword("", "test"); err == nil {
		t.Fatal("empty path accepted")
	}
}
