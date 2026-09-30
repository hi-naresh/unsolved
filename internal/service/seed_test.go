package service_test

import (
	"context"
	"testing"
	"unicode/utf8"

	"github.com/hi-naresh/unsolved/internal/service"
)

func TestSeedProblemsAreValid(t *testing.T) {
	if len(service.SeedProblems) != 5 {
		t.Fatalf("want 5 seed problems, got %d", len(service.SeedProblems))
	}
	domains := map[string]bool{}
	for _, p := range service.SeedProblems {
		if n := utf8.RuneCountInString(p.Title); n < 10 || n > 140 {
			t.Errorf("%q: title length %d", p.Title, n)
		}
		if n := utf8.RuneCountInString(p.CurrentProcess); n < 50 || n > 4000 {
			t.Errorf("%q: current_process length %d", p.Title, n)
		}
		if n := utf8.RuneCountInString(p.Pain); n < 20 || n > 2000 {
			t.Errorf("%q: pain length %d", p.Title, n)
		}
		if n := utf8.RuneCountInString(p.Tried); n > 2000 {
			t.Errorf("%q: tried length %d", p.Title, n)
		}
		domains[p.Domain] = true
	}
	if len(domains) != 5 {
		t.Errorf("seed problems should span 5 domains, got %d", len(domains))
	}
}

func TestSeedIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := modSvc(t)
	n, err := s.Seed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("first run created %d, want 5", n)
	}
	n, err = s.Seed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second run created %d, want 0", n)
	}
	var count, withCurrent int
	err = s.Store.Pool.QueryRow(ctx, `SELECT count(*), count(p.current_revision_id)
		FROM problems p JOIN users u ON u.id = p.author_id
		WHERE p.is_seed AND u.handle = $1 AND p.author_display = 'named'`, service.SeedHandle).Scan(&count, &withCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 || withCurrent != 5 {
		t.Fatalf("seed problems = %d (with current revision %d), want 5", count, withCurrent)
	}
	var name string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT display_name FROM users WHERE handle = $1`, service.SeedHandle).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != service.SeedDisplayName {
		t.Fatalf("system author display name = %q", name)
	}
}
