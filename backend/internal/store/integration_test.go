//go:build integration

package store

import (
	"context"
	"os"
	"testing"
)

// TestMigrationsUpDown verifies the full migration chain applies and rolls back
// cleanly. Requires TEST_DATABASE_URL (CI service container or local dev compose).
func TestMigrationsUpDown(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; start dev postgres via `make dev-up` and export it")
	}
	ctx := context.Background()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	defer pool.Close()

	if _, err := MigrateUp(ctx, pool); err != nil {
		t.Fatalf("up: %v", err)
	}
	applied, err := AppliedVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	migs, _ := LoadMigrations()
	if len(applied) != len(migs) {
		t.Fatalf("applied %d, expected %d", len(applied), len(migs))
	}

	total := len(migs)
	for i := 0; i < total; i++ {
		if _, err := MigrateDown(ctx, pool, 1); err != nil {
			t.Fatalf("down step %d: %v", i+1, err)
		}
	}
	left, err := AppliedVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("expected clean database after down-all, left %d", len(left))
	}

	if _, err := MigrateUp(ctx, pool); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}
