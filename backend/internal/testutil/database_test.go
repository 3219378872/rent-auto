package testutil

import "testing"

func TestValidateDatabaseURL(t *testing.T) {
	for _, tc := range []struct {
		url     string
		allowed bool
	}{
		{"", false}, {"postgres://localhost/rentauto", false}, {"postgres://localhost/production", false},
		{"postgres://localhost/rentauto_test", true}, {"postgres://localhost/test_review", true},
		{"host=localhost dbname=test_integration", true}, {"postgres://[invalid", false},
	} {
		if got := ValidateDatabaseURL(tc.url) == nil; got != tc.allowed {
			t.Errorf("url %q allowed=%v want %v", tc.url, got, tc.allowed)
		}
	}
}
