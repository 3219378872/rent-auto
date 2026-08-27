package domain

import (
	"strings"
	"testing"
)

// Session IDs gate who may complete a UU login challenge; the generator must
// stay random (crypto/rand) and collision-free at panel scale.
func TestRandomSessionID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s := RandomSessionID()
		if len(s) != 16 {
			t.Fatalf("len=%d want 16", len(s))
		}
		for _, r := range s {
			if !strings.ContainsRune(sessionAlphabet, r) {
				t.Fatalf("unexpected rune %q in %q", r, s)
			}
		}
		if seen[s] {
			t.Fatalf("collision on %q after %d draws", s, i)
		}
		seen[s] = true
	}
}
