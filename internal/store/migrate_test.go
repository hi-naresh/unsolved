package store_test

import (
	"context"
	"testing"

	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
)

func TestMigrationsRollBackAndReapply(t *testing.T) {
	ctx := context.Background()
	url := storetest.URL(t) // already migrated up
	if err := store.Migrate(ctx, url, "reset"); err != nil {
		t.Fatalf("down to zero: %v", err)
	}
	if err := store.Migrate(ctx, url, "up"); err != nil {
		t.Fatalf("up again: %v", err)
	}
	domains, err := storetest.Store(t).ListDomains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 10 {
		t.Fatalf("want 10 seeded domains, got %d", len(domains))
	}
}
