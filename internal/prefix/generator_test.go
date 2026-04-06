package prefix

import (
	"strings"
	"testing"
)

func TestGenerate_MinLength(t *testing.T) {
	g := New()
	for range 1000 {
		p := g.Generate()
		if len(p) < 3 {
			t.Fatalf("generated prefix %q is shorter than 3 chars", p)
		}
	}
}

func TestGenerate_ValidChars(t *testing.T) {
	g := New()
	for range 1000 {
		p := g.Generate()
		for _, c := range p {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-') {
				t.Fatalf("prefix %q contains invalid char %q", p, string(c))
			}
		}
	}
}

func TestGenerateN_Unique(t *testing.T) {
	g := New()
	results := g.GenerateN(100)
	seen := make(map[string]struct{})
	for _, r := range results {
		if _, dup := seen[r]; dup {
			t.Fatalf("duplicate prefix: %q", r)
		}
		seen[r] = struct{}{}
	}
	if len(results) != 100 {
		t.Fatalf("expected 100 results, got %d", len(results))
	}
}

func TestGenerateForDomain(t *testing.T) {
	g := New()
	addr := g.GenerateForDomain("example.com")
	if !strings.HasSuffix(addr, "@example.com") {
		t.Fatalf("address %q should end with @example.com", addr)
	}
	parts := strings.SplitN(addr, "@", 2)
	if len(parts[0]) < 3 {
		t.Fatalf("prefix %q is too short", parts[0])
	}
}

func TestGenerate_Distribution(t *testing.T) {
	g := New()
	counts := map[string]int{"dot": 0, "under": 0, "dash": 0, "plain": 0}
	n := 10000
	for range n {
		p := g.Generate()
		switch {
		case strings.Contains(p, "."):
			counts["dot"]++
		case strings.Contains(p, "_"):
			counts["under"]++
		case strings.Contains(p, "-"):
			counts["dash"]++
		default:
			counts["plain"]++
		}
	}
	// Dots should be most common (used in first.last and adj.noun patterns)
	if counts["dot"] < n/4 {
		t.Errorf("dot count %d seems too low out of %d", counts["dot"], n)
	}
}

func TestGenerate_SamplesLookHuman(t *testing.T) {
	g := New()
	t.Log("Sample generated prefixes:")
	for range 20 {
		t.Log("  ", g.GenerateForDomain("example.com"))
	}
}
