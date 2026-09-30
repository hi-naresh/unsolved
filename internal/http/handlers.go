// Package http is the chi router, middleware and handlers (one file per
// resource). Handlers call services only; they never touch the store.
package http

import (
	"log/slog"

	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/service"
)

type Handlers struct {
	Svc    *service.Service
	Auth   *auth.Auth
	Cfg    config.Config
	Log    *slog.Logger
	Limits *Limiter
}

func NewHandlers(svc *service.Service, a *auth.Auth, cfg config.Config, log *slog.Logger) *Handlers {
	return &Handlers{Svc: svc, Auth: a, Cfg: cfg, Log: log, Limits: NewLimiter(log)}
}
