// Package config loads runtime configuration from environment variables.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	Addr          string
	DatabaseURL   string
	JWTSecret     []byte
	JWTTTL        time.Duration
	MasterKey     []byte // 32B AES-GCM key; empty disables credential encryption (dev only)
	AdminUser     string
	AdminPassHash string // bcrypt hash from env; empty -> bootstrap flow
	DryRunDefault bool
	LogLevel      string
}

func Load() (*Config, error) {
	c := &Config{
		Addr:          envOr("ADDR", ":8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		JWTTTL:        24 * time.Hour,
		AdminUser:     envOr("ADMIN_USER", "admin"),
		AdminPassHash: os.Getenv("ADMIN_PASSWORD_HASH"),
		DryRunDefault: os.Getenv("DRY_RUN_DEFAULT") != "false",
		LogLevel:      strings.ToLower(envOr("LOG_LEVEL", "info")),
	}
	if c.DatabaseURL == "" {
		return nil, errors.New("config: DATABASE_URL is required")
	}
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 {
		return nil, errors.New("config: JWT_SECRET must be at least 32 bytes")
	}
	c.JWTSecret = []byte(secret)

	mk := os.Getenv("APP_MASTER_KEY")
	if mk != "" {
		key, err := hex.DecodeString(mk)
		if err != nil || len(key) != 32 {
			return nil, errors.New("config: APP_MASTER_KEY must be 32-byte hex")
		}
		c.MasterKey = key
	}
	return c, nil
}

// BootstrapPassword generates a random admin password for first-run bootstrap.
func BootstrapPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("bootstrap password: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
