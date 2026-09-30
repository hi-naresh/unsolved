package http

import (
	"errors"
	"net/http"

	"github.com/getsentry/sentry-go"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/views/pages"
)

// handlerFunc is an http handler that returns an error; wrap maps it.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

var errNotFoundPage = service.ErrNotFound

// wrap is the one place service errors become HTTP status codes:
// ErrValidation → 422, ErrNotFound → 404, ErrForbidden → 403,
// ErrConflict → 409, anything else → 500 (logged and sent to Sentry).
func (h *Handlers) wrap(fn handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := fn(w, r)
		if err == nil {
			return
		}
		status, title, msg := http.StatusInternalServerError, "Something went wrong", "We've been told about it. Try again in a moment."
		var ve service.ErrValidation
		switch {
		case errors.As(err, &ve):
			status, title, msg = http.StatusUnprocessableEntity, "That didn't work", ve.Msg
		case errors.Is(err, service.ErrNotFound):
			status, title, msg = http.StatusNotFound, "Not found", "There's nothing here."
		case errors.Is(err, service.ErrForbidden):
			status, title, msg = http.StatusForbidden, "Not allowed", "You can't do that."
		case errors.Is(err, service.ErrConflict):
			status, title, msg = http.StatusConflict, "Conflict", "That conflicts with something that already exists."
		default:
			h.Log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err)
			if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
				hub.CaptureException(err)
			} else {
				sentry.CaptureException(err)
			}
		}
		if isHX(r) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(msg))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_ = pages.Message(title, msg).Render(r.Context(), w)
	}
}
