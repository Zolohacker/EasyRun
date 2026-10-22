package store

import "testing"

func TestValidID(t *testing.T) {
	for _, id := range []string{"run_123", "route-aBc"} {
		if !validID(id) {
			t.Fatalf("expected valid ID: %q", id)
		}
	}
	for _, id := range []string{"", "../run", "a/b", "run.json"} {
		if validID(id) {
			t.Fatalf("expected invalid ID: %q", id)
		}
	}
}
