// Command server runs the HTTP server and the River workers in one process.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/config"
	unhttp "github.com/hi-naresh/unsolved/internal/http"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

const (
	publicAddr  = ":8080"
	metricsAddr = ":9091" // internal only; not exposed in fly.toml services
	staticDir   = "static"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{Dsn: cfg.SentryDSN}); err != nil {
			return err
		}
		defer sentry.Flush(2 * time.Second)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := store.Connect(ctx, cfg.DatabaseURL, 20)
	if err != nil {
		return err
	}
	defer pool.Close()
	st := store.Open(pool)
	scorer := ranking.NewDecayScorer(cfg.RankHalfLife)

	riverClient, err := river.NewClient(riverpgxv5.New(pool), jobs.Config(jobs.Deps{
		Store: st, Scorer: scorer, Cfg: cfg, Log: log,
	}))
	if err != nil {
		return err
	}

	svc := service.New(st, riverClient, scorer, service.NewWeigher(st, cfg), cfg, log)
	a := auth.New(cfg, svc, log)
	h := unhttp.NewHandlers(svc, a, cfg, log)

	srv := &http.Server{Addr: publicAddr, Handler: h.Router(staticDir), ReadHeaderTimeout: 10 * time.Second}
	metrics := &http.Server{Addr: metricsAddr, Handler: promhttp.Handler(), ReadHeaderTimeout: 10 * time.Second}

	if err := riverClient.Start(ctx); err != nil {
		return err
	}
	errc := make(chan error, 2)
	go func() { errc <- listen(srv) }()
	go func() { errc <- listen(metrics) }()
	log.Info("listening", "addr", publicAddr, "metrics", metricsAddr)

	select {
	case <-ctx.Done():
	case err := <-errc:
		log.Error("listener failed", "err", err)
	}

	// SIGTERM: stop accepting, finish in-flight within 10 s, stop River cleanly.
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = metrics.Shutdown(shutdownCtx)
	if err := riverClient.Stop(shutdownCtx); err != nil {
		log.Error("river stop", "err", err)
	}
	return nil
}

func listen(s *http.Server) error {
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
