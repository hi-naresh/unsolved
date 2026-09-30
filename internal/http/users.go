package http

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/views/pages"
)

// mountUsers: /welcome (new members), public profiles and the directory.
func (h *Handlers) mountUsers(r chi.Router) {
	r.With(auth.RequireUser).Get("/welcome", h.wrap(h.welcomePage))
	// Changing the handle is a profile edit: suspended accounts can't.
	r.With(auth.RequireWriter).Post("/welcome", h.wrap(h.welcomeSubmit))
	r.Get("/u/{handle}", h.wrap(h.profilePage))
	r.Get("/members", h.wrap(h.membersPage))
}

func (h *Handlers) welcomePage(w http.ResponseWriter, r *http.Request) error {
	u, err := h.Svc.GetUser(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	f := pages.WelcomeForm{Handle: u.Handle, InDirectory: u.InDirectory, Next: auth.SafeNext(r.URL.Query().Get("next"))}
	return h.render(w, r, http.StatusOK, pages.Welcome(f), nil)
}

func (h *Handlers) welcomeSubmit(w http.ResponseWriter, r *http.Request) error {
	f := pages.WelcomeForm{
		Handle:      r.PostFormValue("handle"),
		InDirectory: r.PostFormValue("in_directory") != "",
		Next:        auth.SafeNext(r.PostFormValue("next")),
	}
	err := h.Svc.CompleteWelcome(r.Context(), auth.UserFrom(r.Context()).ID, f.Handle, f.InDirectory)
	var ve service.ErrValidation
	if errors.As(err, &ve) {
		f.Errors = map[string]string{ve.Field: ve.Msg}
		return h.render(w, r, http.StatusUnprocessableEntity, pages.Welcome(f), nil)
	}
	if err != nil {
		return err
	}
	redirect(w, r, f.Next)
	return nil
}

func (h *Handlers) profilePage(w http.ResponseWriter, r *http.Request) error {
	p, err := h.Svc.ProfileByHandle(r.Context(), chi.URLParam(r, "handle"))
	if err != nil {
		return err
	}
	return h.render(w, r, http.StatusOK, pages.Profile(p), nil)
}

func (h *Handlers) membersPage(w http.ResponseWriter, r *http.Request) error {
	members, next, err := h.Svc.Directory(r.Context(), r.URL.Query().Get("after"))
	if err != nil {
		return err
	}
	return h.render(w, r, http.StatusOK, pages.Members(members, next), pages.MembersList(members, next))
}
