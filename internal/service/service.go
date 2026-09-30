// Package service holds the business rules. Handlers call services; services
// call the store. One file per domain.
package service

import (
	"errors"
	"log/slog"
	"time"

	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

type Service struct {
	Store   *store.Store
	Jobs    *river.Client[pgx.Tx] // used only for InsertTx inside store.InTx
	Scorer  ranking.Scorer
	Weigher ranking.VoteWeigher
	Cfg     config.Config
	Log     *slog.Logger
	Now     func() time.Time // injectable clock; always UTC
}

func New(st *store.Store, jobs *river.Client[pgx.Tx], scorer ranking.Scorer, weigher ranking.VoteWeigher, cfg config.Config, log *slog.Logger) *Service {
	return &Service{
		Store:   st,
		Jobs:    jobs,
		Scorer:  scorer,
		Weigher: weigher,
		Cfg:     cfg,
		Log:     log,
		Now:     func() time.Time { return time.Now().UTC() },
	}
}

// notFound maps pgx.ErrNoRows to ErrNotFound and passes other errors through.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
