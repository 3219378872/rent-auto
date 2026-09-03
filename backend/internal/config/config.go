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
	Addr        string
	DatabaseURL string
	JWTSecret   []byte
	JWTTTL      time.Duration
	MasterKey   []byte // 32B AES-GCM key; empty disables credential encryption (dev only).
	// NOTE: empty MasterKey intentionally still loads — fail-closed is
	// enforced at the credential-write path (registry refuses to seal and
	// the handlers answer 500“APP_MASTER_KEY未配置，三渠道不可用”). Making
	// Load() itself fail would break dev bootstrap and existing callers.
	AdminUser     string
	AdminPassHash string // bcrypt hash from env; empty -> bootstrap flow
	DryRunDefault bool   // mirrors env DRY_RUN_DEFAULT
	LogLevel      string
	TrustProxies  []string // CIDR list allowed to set X-Real-IP (empty = API default private ranges)
}

func Load() (*Config, error) {
	c := &Config{
		Addr:          envOr("ADDR", ":8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		JWTTTL:        parseTTL(envOr("JWT_TTL", "24h")),
		AdminUser:     envOr("ADMIN_USER", "admin"),
		AdminPassHash: os.Getenv("ADMIN_PASSWORD_HASH"),
		DryRunDefault: os.Getenv("DRY_RUN_DEFAULT") != "false",
		LogLevel:      strings.ToLower(envOr("LOG_LEVEL", "info")),
	}
	if v := os.Getenv("TRUST_PROXY_CIDRS"); v != "" {
		for _, cidr := range strings.Split(v, ",") {
			if cidr = strings.TrimSpace(cidr); cidr != "" {
				c.TrustProxies = append(c.TrustProxies, cidr)
			}
		}
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

// parseTTL parses JWT_TTL (e.g. "12h"); unparseable/non-positive values fall
// back to 24h, and anything above 24h is clamped to the spec ceiling.
func parseTTL(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 24 * time.Hour
	}
	if d > 24*time.Hour {
		return 24 * time.Hour
	}
	return d
}
