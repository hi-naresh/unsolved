package http

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/hi-naresh/unsolved/internal/auth"
	"golang.org/x/time/rate"
)

// Action names for rate limits. Limits are per user id, or per IP when signed
// out, in-process (two machines means up to double; accepted).
const (
	ActNewProblem  = "new_problem"
	ActNewRevision = "new_revision" // problem or solution
	ActNewSolution = "new_solution"
	ActVote        = "vote"
	ActReport      = "report"
	ActSignIn      = "sign_in" // always keyed by IP
)

type limitSpec struct {
	n   int
	per time.Duration
}

var limitSpecs = map[string]limitSpec{
	ActNewProblem:  {3, 24 * time.Hour},
	ActNewRevision: {20, 24 * time.Hour},
	ActNewSolution: {10, 24 * time.Hour},
	ActVote:        {120, time.Hour},
	ActReport:      {20, 24 * time.Hour},
	ActSignIn:      {20, time.Hour},
}

type limiterEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

// Limiter holds one token bucket per (action, key).
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*limiterEntry
	log     *slog.Logger
}

func NewLimiter(log *slog.Logger) *Limiter {
	return &Limiter{buckets: map[string]*limiterEntry{}, log: log}
}

// Allow takes one token for action/key.
func (l *Limiter) Allow(action, key string) bool {
	spec, ok := limitSpecs[action]
	if !ok {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	k := action + "|" + key
	e := l.buckets[k]
	if e == nil {
		e = &limiterEntry{lim: rate.NewLimiter(rate.Every(spec.per/time.Duration(spec.n)), spec.n)}
		l.buckets[k] = e
	}
	e.seen = now
	if len(l.buckets) > 100_000 {
		l.sweep(now)
	}
	return e.lim.AllowN(now, 1)
}

// sweep drops buckets idle for longer than a day (by then they're full again).
func (l *Limiter) sweep(now time.Time) {
	for k, e := range l.buckets {
		if now.Sub(e.seen) > 25*time.Hour {
			delete(l.buckets, k)
		}
	}
}

// limit wraps a handler with the named rate limit. Over limit → 429 and a
// plain message, logged at WARN.
func (h *Handlers) limit(action string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "ip:" + clientIP(r)
			if u := auth.UserFrom(r.Context()); u != nil && action != ActSignIn {
				key = "user:" + u.ID.String()
			}
			if !h.Limits.Allow(action, key) {
				h.Log.WarnContext(r.Context(), "rate limited", "action", action, "key", key)
				http.Error(w, "You've hit the limit for this action. Try again later.", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
