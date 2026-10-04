package http

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/views/pages"
)

// mountSettings: profile settings, data export and account deletion.
// Suspended users may view settings, export and delete, but not edit.
func (h *Handlers) mountSettings(r chi.Router) {
	r.With(auth.RequireUser).Get("/settings", h.wrap(h.settingsPage))
	r.With(auth.RequireWriter).Post("/settings", h.wrap(h.settingsSubmit))
	r.With(auth.RequireUser).Get("/settings/export", h.wrap(h.settingsExport))
	r.With(auth.RequireUser).Get("/settings/delete", h.wrap(h.settingsDeletePage))
	r.With(auth.RequireUser).Post("/settings/delete", h.wrap(h.settingsDelete))
}

func (h *Handlers) settingsPage(w http.ResponseWriter, r *http.Request) error {
	viewer := auth.UserFrom(r.Context())
	u, err := h.Svc.GetUser(r.Context(), viewer.ID)
	if err != nil {
		return err
	}
	st := service.SettingsOf(u)
	f := pages.SettingsForm{
		Handle: st.Handle, InDirectory: st.InDirectory, DeclaredHistory: st.DeclaredHistory, Email: st.Email,
		ContributionPreference: st.ContributionPreference, OnboardingCompleted: u.OnboardingCompleted,
		Suspended: viewer.Suspended, Saved: r.URL.Query().Get("saved") == "1",
	}
	return h.render(w, r, http.StatusOK, pages.Settings(f), nil)
}

func (h *Handlers) settingsSubmit(w http.ResponseWriter, r *http.Request) error {
	in := service.Settings{
		Handle:                 r.PostFormValue("handle"),
		InDirectory:            r.PostFormValue("in_directory") != "",
		DeclaredHistory:        r.PostFormValue("declared_history"),
		Email:                  r.PostFormValue("email"),
		ContributionPreference: r.PostFormValue("contribution_preference"),
	}
	err := h.Svc.UpdateSettings(r.Context(), auth.UserFrom(r.Context()).ID, in)
	var ve service.ErrValidation
	if errors.As(err, &ve) {
		f := pages.SettingsForm{
			Handle: in.Handle, InDirectory: in.InDirectory, DeclaredHistory: in.DeclaredHistory, Email: in.Email,
			ContributionPreference: in.ContributionPreference,
			Errors:                 map[string]string{ve.Field: ve.Msg},
		}
		return h.render(w, r, http.StatusUnprocessableEntity, pages.Settings(f), nil)
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/settings?saved=1")
	return nil
}

func (h *Handlers) settingsExport(w http.ResponseWriter, r *http.Request) error {
	data, err := h.Svc.ExportUserData(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="unsolved-export.json"`)
	w.Header().Set("Cache-Control", "no-store")
	_, err = w.Write(data)
	return err
}

func (h *Handlers) settingsDeletePage(w http.ResponseWriter, r *http.Request) error {
	return h.render(w, r, http.StatusOK, pages.DeleteAccount(auth.UserFrom(r.Context()).Handle), nil)
}

func (h *Handlers) settingsDelete(w http.ResponseWriter, r *http.Request) error {
	if err := h.Svc.DeleteAccount(r.Context(), auth.UserFrom(r.Context()).ID); err != nil {
		return err
	}
	// The session rows are already gone; this clears the cookie.
	if err := h.Auth.EndSession(w, r); err != nil {
		return err
	}
	redirect(w, r, "/")
	return nil
}
