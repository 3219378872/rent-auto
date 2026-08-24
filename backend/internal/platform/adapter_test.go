package platform

import (
	"errors"
	"testing"
)

func TestSentinelErrorsDistinct(t *testing.T) {
	sentinels := []error{ErrUnsupported, ErrAuthExpired, ErrRateLimited, ErrPlatformBlocked, ErrPartialFailure}
	seen := map[string]bool{}
	for i, e := range sentinels {
		if e == nil || e.Error() == "" {
			t.Fatalf("sentinel %d empty", i)
		}
		if seen[e.Error()] {
			t.Fatalf("duplicate sentinel text: %s", e.Error())
		}
		seen[e.Error()] = true
	}
}

func TestPartialErrorMessage(t *testing.T) {
	e := &PartialError{Ref: "asset-9", Msg: "rejected"}
	want := "platform: item asset-9: rejected"
	if e.Error() != want {
		t.Fatalf("%q want %q", e.Error(), want)
	}
	var base error = e
	if !errors.Is(base, ErrPartialFailure) && base.Error() != want {
		t.Fatal("wrapping semantics broken")
	}
}

// Every *PartialError must satisfy errors.Is(err, ErrPartialFailure) so
// callers can branch on the sentinel uniformly across channels.
func TestPartialErrorUnwrapsToSentinel(t *testing.T) {
	var err error = &PartialError{Ref: "G1", Msg: "busy"}
	if !errors.Is(err, ErrPartialFailure) {
		t.Fatal("PartialError must unwrap to ErrPartialFailure")
	}
}
