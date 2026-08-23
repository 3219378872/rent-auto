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
		if !strings.Contains(m.UpSQL, "CREATE TABLE") && !strings.Contains(m.UpSQL, "CREATE INDEX") {
			t.Fatalf("migration %s up sql looks empty", m.Version)
		}
		if m.DownSQL == "" {
			t.Fatalf("migration %s missing down", m.Version)
		}
	}
}
