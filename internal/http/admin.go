package http

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/views/pages"
)

// mountAdmin: /admin and its moderation actions. Admins are ADMIN_HANDLES;
// everyone else gets 404. Every action is logged (WARN) or writes a
// problem_state_events row, with the admin's id.
func (h *Handlers) mountAdmin(r chi.Router) {
	r.Route("/admin", func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/", h.wrap(h.adminPage))
		r.Post("/reports/{id}/resolve", h.wrap(h.adminResolveReport))
		r.Post("/problems/{id}/invalid", h.wrap(h.adminMarkInvalid))
		r.Post("/problems/{id}/reopen", h.wrap(h.adminReopen))
		r.Post("/problems/{id}/meta", h.wrap(h.adminToggleMeta))
		r.Post("/users/{handle}/suspend", h.wrap(h.adminSuspend(true)))
		r.Post("/users/{handle}/unsuspend", h.wrap(h.adminSuspend(false)))
		r.Post("/users/{handle}/zero-votes", h.wrap(h.adminZeroVotes))
		r.Post("/rescore", h.wrap(h.adminRescore))
	})
}

func (h *Handlers) adminPage(w http.ResponseWriter, r *http.Request) error {
	rows, next, err := h.Svc.ListOpenReports(r.Context(), r.URL.Query().Get("after"))
	if err != nil {
		return err
	}
	now := h.Svc.Now()
	reports := make([]pages.AdminReport, 0, len(rows))
	for _, row := range rows {
		reports = append(reports, adminReportView(row, now))
	}
	page := pages.Admin(reports, next)
	return h.render(w, r, http.StatusOK, page, nil)
}

func adminReportView(row store.AdminListOpenReportsRow, now time.Time) pages.AdminReport {
	v := pages.AdminReport{
		ID:           row.ID.String(),
		Kind:         row.TargetKind,
		Title:        row.TargetTitle,
		Excerpt:      row.TargetExcerpt,
		ProblemID:    row.TargetProblemID,
		ProblemState: row.ProblemState,
		OnMeta:       row.ProblemOnMeta,
		Anonymous:    row.TargetAnonymous,
		Handle:       row.TargetHandle,
		Suspended:    row.TargetSuspended,
		Deleted:      row.TargetDeleted,
		Reporter:     row.ReporterHandle,
		Reason:       row.Reason,
		Age:          adminAge(now.Sub(row.CreatedAt)),
	}
	if v.Anonymous {
		v.Handle = "" // the query already blanks it; belt and braces
	}
	return v
}

// adminAge formats a duration as a short age ("5m ago", "3h ago", "2d ago").
func adminAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// adminPathID parses a uuid URL parameter; a bad id is a 404.
func adminPathID(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, service.ErrNotFound
	}
	return id, nil
}

func adminActor(r *http.Request) uuid.UUID { return auth.UserFrom(r.Context()).ID }

func (h *Handlers) adminResolveReport(w http.ResponseWriter, r *http.Request) error {
	id, err := adminPathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.ResolveReport(r.Context(), adminActor(r), id); err != nil {
		return err
	}
	redirect(w, r, "/admin")
	return nil
}

func (h *Handlers) adminMarkInvalid(w http.ResponseWriter, r *http.Request) error {
	id, err := adminPathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.AdminMarkInvalid(r.Context(), adminActor(r), id, r.PostFormValue("reason")); err != nil {
		return err
	}
	redirect(w, r, "/admin")
	return nil
}

func (h *Handlers) adminReopen(w http.ResponseWriter, r *http.Request) error {
	id, err := adminPathID(r, "id")
	if err != nil {
		return err
	}
	if err := h.Svc.AdminReopen(r.Context(), adminActor(r), id, r.PostFormValue("reason")); err != nil {
		return err
	}
	redirect(w, r, "/admin")
	return nil
}

func (h *Handlers) adminToggleMeta(w http.ResponseWriter, r *http.Request) error {
	id, err := adminPathID(r, "id")
	if err != nil {
		return err
	}
	if _, err := h.Svc.AdminToggleMeta(r.Context(), adminActor(r), id); err != nil {
		return err
	}
	redirect(w, r, "/admin")
	return nil
}

func (h *Handlers) adminSuspend(suspend bool) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := h.Svc.AdminSetSuspended(r.Context(), adminActor(r), chi.URLParam(r, "handle"), suspend); err != nil {
			return err
		}
		redirect(w, r, "/admin")
		return nil
	}
}

func (h *Handlers) adminZeroVotes(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := h.Svc.AdminZeroVotes(r.Context(), adminActor(r), chi.URLParam(r, "handle")); err != nil {
		return err
	}
	redirect(w, r, "/admin")
	return nil
}

func (h *Handlers) adminRescore(w http.ResponseWriter, r *http.Request) error {
	if err := h.Svc.AdminRescoreAll(r.Context(), adminActor(r)); err != nil {
		return err
	}
	redirect(w, r, "/admin")
	return nil
}
