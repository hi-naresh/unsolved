package http

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/views/partials"
)

// mountReports: POST /report — any signed-in, non-suspended user can report a
// problem revision, solution revision or user.
func (h *Handlers) mountReports(r chi.Router) {
	r.With(auth.RequireWriter, h.limit(ActReport)).Post("/report", h.wrap(h.createReport))
}

func (h *Handlers) createReport(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	if err := h.Svc.CreateReport(r.Context(), u.ID,
		r.PostFormValue("target_kind"), r.PostFormValue("target_id"), r.PostFormValue("reason")); err != nil {
		return err
	}
	if isHX(r) {
		return h.render(w, r, http.StatusOK, partials.ReportThanks(), partials.ReportThanks())
	}
	redirect(w, r, h.reportBackURL(r))
	return nil
}

// reportBackURL returns the Referer's path if it points at this site, else "/".
func (h *Handlers) reportBackURL(r *http.Request) string {
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Host == "" {
		return "/"
	}
	ok := ref.Host == r.Host
	if base, err := url.Parse(h.Cfg.BaseURL); err == nil && base.Host != "" && ref.Host == base.Host {
		ok = true
	}
	if !ok {
		return "/"
	}
	back := ref.EscapedPath()
	if back == "" || back[0] != '/' || (len(back) > 1 && (back[1] == '/' || back[1] == '\\')) {
		return "/"
	}
	if ref.RawQuery != "" {
		back += "?" + ref.RawQuery
	}
	return back
}
