// Package storetest starts a real Postgres (pgvector/pgvector:pg16) with
// testcontainers-go, once per test package, and applies the migrations fresh.
// There are no database mocks in this codebase.
package storetest

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	once    sync.Once
	dsn     string
	pool    *pgxpool.Pool
	initErr error
)

// URL returns the connection string of the package's migrated test database.
func URL(t testing.TB) string {
	t.Helper()
	start(t)
	return dsn
}

// Pool returns a pool connected to the package's migrated test database.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	start(t)
	return pool
}

// Store returns a Store over Pool(t).
func Store(t testing.TB) *store.Store {
	t.Helper()
	return store.Open(Pool(t))
}

func start(t testing.TB) {
	t.Helper()
	once.Do(func() {
		ctx := context.Background()
		if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
			// Escape hatch for machines without Docker: must be an empty database.
			dsn = url
		} else {
			c, err := postgres.Run(ctx, "pgvector/pgvector:pg16",
				postgres.WithDatabase("unsolved_test"),
				postgres.WithUsername("unsolved"),
				postgres.WithPassword("unsolved"),
				testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(60*time.Second)),
			)
			if err != nil {
				initErr = err
				return
			}
			// The container is removed by testcontainers' reaper when the test binary exits.
			dsn, initErr = c.ConnectionString(ctx, "sslmode=disable")
			if initErr != nil {
				return
			}
		}
		if initErr = store.Migrate(ctx, dsn, "up"); initErr != nil {
			return
		}
		pool, initErr = store.Connect(ctx, dsn, 10)
	})
	if initErr != nil {
		t.Fatalf("storetest: %v", initErr)
	}
}
