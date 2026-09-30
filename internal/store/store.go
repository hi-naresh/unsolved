package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the pool and the sqlc queries. It is the only thing in the
// codebase that talks to Postgres.
type Store struct {
	Pool *pgxpool.Pool
	*Queries
}

// Open wraps a pool. (sqlc's generated constructor already owns the name New.)
func Open(pool *pgxpool.Pool) *Store {
	return &Store{Pool: pool, Queries: New(pool)}
}

// Connect opens a pgxpool. maxConns is 20 per machine in production.
func Connect(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// InTx runs fn inside one transaction. fn receives queries bound to the tx and
// the tx itself (for river.InsertTx). The tx commits if fn returns nil.
func (s *Store) InTx(ctx context.Context, fn func(q *Queries, tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		return fn(s.Queries.WithTx(tx), tx)
	})
}
