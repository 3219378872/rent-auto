package store

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/3219378872/rent-auto/backend/migrations"
)

// Migration is one versioned migration pair loaded from backend/migrations.
type Migration struct {
	Version string
	UpSQL   string
	DownSQL string
}

// LoadMigrations parses the embedded SQL files. Version = filename prefix before first "_".
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("glob migrations: %w", err)
	}
	ups := map[string]string{}
	downs := map[string]string{}
	var versions []string
	for _, name := range entries {
		base := path.Base(name)
		stem := strings.TrimSuffix(base, ".sql")
		var ver, direction string
		switch {
		case strings.HasSuffix(stem, ".up"):
			ver, direction = strings.TrimSuffix(stem, ".up"), "up"
		case strings.HasSuffix(stem, ".down"):
			ver, direction = strings.TrimSuffix(stem, ".down"), "down"
		default:
			return nil, fmt.Errorf("migration %q must end with .up.sql or .down.sql", base)
		}
		b, err := migrations.FS.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", base, err)
		}
		if _, ok := ups[ver]; !ok && direction == "up" {
			versions = append(versions, ver)
		}
		if direction == "up" {
			ups[ver] = string(b)
		} else {
			downs[ver] = string(b)
		}
	}
	sort.Strings(versions)
	out := make([]Migration, 0, len(versions))
	for _, v := range versions {
		out = append(out, Migration{Version: v, UpSQL: ups[v], DownSQL: downs[v]})
	}
	return out, nil
}
