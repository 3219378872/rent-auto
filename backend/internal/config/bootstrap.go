package config

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ReadBootstrapPassword recovers the file-to-database bootstrap handoff after
// a crash. It is used only while the database has no administrator hash.
func ReadBootstrapPassword(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return "", fmt.Errorf("bootstrap file must be a regular file with permissions 0600")
	}
	f, err := os.Open(path) // #nosec G304 -- deployment-owned path; regular 0600 file and identity checked above/below.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) {
		return "", fmt.Errorf("bootstrap file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, 38))
	if err != nil {
		return "", err
	}
	password := strings.TrimSuffix(string(data), "\n")
	decoded, err := hex.DecodeString(password)
	if err != nil || len(password) != 36 || hex.EncodeToString(decoded) != password {
		return "", fmt.Errorf("bootstrap file does not contain a generated initial password")
	}
	return password, nil
}

// WriteBootstrapPassword never truncates an existing file or follows a final
// symlink. The file is a restricted one-time delivery channel, not a log.
func WriteBootstrapPassword(path, password string) error {
	if path == "" {
		return fmt.Errorf("ADMIN_BOOTSTRAP_FILE is required for password bootstrap")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create bootstrap directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600) // #nosec G304 -- deployment-owned path; exclusive creation prevents overwrite or final symlink following.
	if err != nil {
		return fmt.Errorf("create bootstrap password file (existing files are never overwritten): %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(password + "\n"); err != nil {
		return fmt.Errorf("write bootstrap password file: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync bootstrap password file: %w", err)
	}
	return f.Close()
}

func defaultBootstrapFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "rent-auto", "admin-password")
}
