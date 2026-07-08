package dnsmasq

import "testing"

// The registry drives every form in the UI; a malformed entry renders as a
// broken card, so structural invariants are worth locking down.
func TestRegistryInvariants(t *testing.T) {
	cats := map[string]bool{}
	for _, c := range Categories {
		if cats[c.ID] {
			t.Errorf("duplicate category %q", c.ID)
		}
		cats[c.ID] = true
	}

	seen := map[string]bool{}
	for _, d := range Registry {
		if d.Key == "" {
			t.Fatal("directive with empty key")
		}
		if seen[d.Key] {
			t.Errorf("duplicate directive %q", d.Key)
		}
		seen[d.Key] = true

		switch d.Kind {
		case KindFlag, KindScalar, KindMulti:
		default:
			t.Errorf("%s: invalid kind %q", d.Key, d.Kind)
		}
		if !cats[d.Cat] {
			t.Errorf("%s: unknown category %q", d.Key, d.Cat)
		}
		if d.Label == "" {
			t.Errorf("%s: missing label", d.Key)
		}
	}
}

func TestLookupDirective(t *testing.T) {
	if d, ok := LookupDirective("server"); !ok || d.Kind != KindMulti {
		t.Fatalf("server must be a multi directive, got %+v ok=%v", d, ok)
	}
	if _, ok := LookupDirective("definitely-not-a-directive"); ok {
		t.Fatal("unknown key must not resolve")
	}
}
