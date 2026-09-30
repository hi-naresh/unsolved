package http

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type ipKey struct{}

// realIP picks the client address: Cloudflare's header first (it fronts
// Fly), then Fly's, then the socket address.
func realIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.Header.Get("CF-Connecting-IP")
		if ip == "" {
			ip = r.Header.Get("Fly-Client-IP")
		}
		if ip == "" {
			ip, _, _ = net.SplitHostPort(r.RemoteAddr)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ipKey{}, ip)))
	})
}

func clientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(ipKey{}).(string); ok && ip != "" {
		return ip
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	return ip
}

// logRequests logs one INFO line per request. Paths may contain content ids
// but never author ids, so anonymous posts can't be linked to authors here.
func (h *Handlers) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		h.Log.InfoContext(r.Context(), "request",
			"method", r.Method, "path", logPath(r.URL.Path), "status", ww.Status(),
			"dur_ms", time.Since(start).Milliseconds(), "req_id", middleware.GetReqID(r.Context()),
			"hx", isHX(r))
	})
}

// recoverPanics turns a panic into a 500 and reports it to Sentry.
func (h *Handlers) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub := sentry.CurrentHub().Clone()
		hub.Scope().SetRequest(r)
		ctx := sentry.SetHubOnContext(r.Context(), hub)
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				hub.RecoverWithContext(ctx, v)
				h.Log.ErrorContext(ctx, "panic", "path", r.URL.Path, "panic", fmt.Sprint(v))
				http.Error(w, "Something went wrong.", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

var httpDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "http_request_duration_seconds",
	Help:    "Server time per request, by route pattern.",
	Buckets: []float64{.005, .01, .025, .05, .1, .2, .5, 1, 2.5, 5},
}, []string{"method", "route", "status"})

func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		httpDuration.WithLabelValues(r.Method, route, strconv.Itoa(ww.Status())).Observe(time.Since(start).Seconds())
	})
}

// logPath keeps bearer-style tokens (the one-tap Solved links) out of logs.
func logPath(p string) string {
	if strings.HasPrefix(p, "/solved/") {
		return "/solved/{token}"
	}
	return p
}
