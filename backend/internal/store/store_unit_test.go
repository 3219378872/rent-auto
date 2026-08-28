package store

import (
	"strings"
	"testing"
)

func TestLoadMigrations(t *testing.T) {
	migs, err := LoadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) == 0 {
		t.Fatal("no migrations loaded")
	}
	prev := ""
	for i, m := range migs {
		if m.Version < prev {
			t.Fatalf("migrations not sorted at index %d", i)
		}
		prev = m.Version
		if !strings.Contains(m.UpSQL, "CREATE TABLE") && !strings.Contains(m.UpSQL, "CREATE INDEX") && !strings.Contains(m.UpSQL, "CREATE UNIQUE INDEX") && !strings.Contains(m.UpSQL, "ALTER TABLE") {
			t.Fatalf("migration %s up sql looks empty", m.Version)
		}
		if m.DownSQL == "" {
			t.Fatalf("migration %s missing down", m.Version)
		}
	}
}

func TestLoadMigrationsRejectsOrphanDown(t *testing.T) {
	// Regression guard for the 2026-08-23 lost-migration incident:
	// a down without its up pair must fail loudly instead of being skipped.
	ups := map[string]string{"0001_x": ""}
	downs := map[string]string{"0001_x": "", "0002_y": ""}
	if err := validatePairMap(ups, downs); err == nil {
		t.Fatal("orphan down must be rejected")
	}
	if err := validatePairMap(map[string]string{"0001_x": ""}, map[string]string{"0001_x": ""}); err != nil {
		t.Fatalf("healthy pair rejected: %v", err)
	}
}
