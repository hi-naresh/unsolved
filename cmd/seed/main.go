// Command seed loads the five launch seed problems (is_seed = true). It is
// idempotent: re-running it skips seeds that already exist.
//
//	seed                                  load seed problems
//	seed admin-handle <current> <admin>   give an ADMIN_HANDLES handle to a user
//
// Admin handles can't be claimed through the site, so an operator assigns them.
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("seed failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := store.Connect(ctx, cfg.DatabaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()
	st := store.Open(pool)
	// Insert-only River client: no workers, never started.
	jobsClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return err
	}
	svc := service.New(st, jobsClient, ranking.NewDecayScorer(cfg.RankHalfLife), service.NewWeigher(st, cfg), cfg, log)
	if len(os.Args) == 4 && os.Args[1] == "admin-handle" {
		return svc.AssignAdminHandle(ctx, os.Args[2], os.Args[3])
	}
	n, err := svc.Seed(ctx)
	if err != nil {
		return err
	}
	log.Info("seed done", "created", n, "total", len(service.SeedProblems))
	return nil
}
