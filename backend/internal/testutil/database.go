package testutil

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ValidateDatabaseURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("TEST_DATABASE_URL is required")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		return errors.New("TEST_DATABASE_URL is invalid")
	}
	name := cfg.ConnConfig.Database
	if !strings.HasPrefix(name, "test_") && !strings.HasSuffix(name, "_test") {
		return errors.New("test database name must start with test_ or end with _test")
	}
	return nil
}

func DatabaseURL(t testing.TB) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL not set; no DATABASE_URL fallback is allowed")
	}
	if err := ValidateDatabaseURL(raw); err != nil {
		t.Fatal(err)
	}
	return raw
}
