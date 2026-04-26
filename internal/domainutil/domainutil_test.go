package domainutil

import (
	"testing"

	"mailapi/internal/model"
)

func TestCandidates(t *testing.T) {
	got := Candidates(" A.B.Example.com ")
	want := []string{"a.b.example.com", "b.example.com", "example.com", "com"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want=%d candidates=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index=%d got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestMatchSet(t *testing.T) {
	allowed := map[string]struct{}{
		"example.com":     {},
		"foo.example.com": {},
	}

	match, ok := MatchSet("bar.foo.example.com", allowed)
	if !ok {
		t.Fatal("expected match")
	}
	if match != "foo.example.com" {
		t.Fatalf("match=%q want=%q", match, "foo.example.com")
	}
}

func TestMatchMapKey(t *testing.T) {
	limits := map[string]int64{
		"example.com": 100,
	}

	match, ok := MatchMapKey("child.example.com", limits)
	if !ok {
		t.Fatal("expected match")
	}
	if match != "example.com" {
		t.Fatalf("match=%q want=%q", match, "example.com")
	}
}

func TestResolveDomain(t *testing.T) {
	domains := []model.Domain{
		{Domain: "example.com", IsActive: true},
		{Domain: "foo.example.com", IsActive: true, IsPrivate: true},
		{Domain: "disabled.example.com", IsActive: false},
	}

	resolved, match, ok := ResolveDomain("bar.foo.example.com", domains)
	if !ok {
		t.Fatal("expected match")
	}
	if match != "foo.example.com" {
		t.Fatalf("match=%q want=%q", match, "foo.example.com")
	}
	if resolved == nil || resolved.Domain != "foo.example.com" || !resolved.IsPrivate {
		t.Fatalf("resolved=%+v", resolved)
	}

	resolved, match, ok = ResolveDomain("mail.example.com", domains)
	if !ok {
		t.Fatal("expected parent match")
	}
	if match != "example.com" {
		t.Fatalf("match=%q want=%q", match, "example.com")
	}
	if resolved == nil || resolved.Domain != "example.com" {
		t.Fatalf("resolved=%+v", resolved)
	}
}
