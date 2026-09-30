package http

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/views/pages"
)

// mountOutcomes: the one-tap outcome link from the SolvedPrompt email. No
// sign-in: the signed token authenticates the poster for this one action.
// GET only shows a confirm page (mail scanners prefetch links); the POST is
// CSRF-protected like every other.
func (h *Handlers) mountOutcomes(r chi.Router) {
	r.Get("/solved/{token}", h.wrap(h.outcomeConfirm))
	r.Post("/solved/{token}", h.wrap(h.outcomeRecord))
}

// outcomeHeaders keeps the token out of caches and Referer headers.
func outcomeHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// outcomeLinkError renders the friendly page for a bad or expired link and
// reports whether err was one of those.
func (h *Handlers) outcomeLinkError(w http.ResponseWriter, r *http.Request, err error) (bool, error) {
	switch {
	case errors.Is(err, service.ErrLinkExpired):
		return true, h.render(w, r, http.StatusGone, pages.Message("This link has expired",
			"Links in our emails work for 14 days. You can still record whether it worked from the problem page after signing in."), nil)
	case errors.Is(err, service.ErrLinkInvalid):
		return true, h.render(w, r, http.StatusBadRequest, pages.Message("This link doesn't work",
			"It may have been cut short by your email app. Try copying the whole link, or record whether it worked from the problem page after signing in."), nil)
	}
	return false, nil
}

func (h *Handlers) outcomeConfirm(w http.ResponseWriter, r *http.Request) error {
	outcomeHeaders(w)
	token := chi.URLParam(r, "token")
	p, err := h.Svc.OutcomePrompt(r.Context(), token)
	if ok, rerr := h.outcomeLinkError(w, r, err); ok {
		return rerr
	}
	if err != nil {
		return err
	}
	v := pages.OutcomeConfirmView{
		Token: token, ProblemID: p.ProblemID.String(), SolutionID: p.SolutionID.String(),
		Title: p.Title, Kind: string(p.Kind), Body: p.Body,
		PosterOutcome: p.PosterOutcome, Solved: p.State == store.ProblemStateSolved,
	}
	if o := store.TriedOutcome(r.URL.Query().Get("outcome")); o.Valid() {
		v.Outcome = string(o)
	}
	return h.render(w, r, http.StatusOK, pages.OutcomeConfirm(v), nil)
}

func (h *Handlers) outcomeRecord(w http.ResponseWriter, r *http.Request) error {
	outcomeHeaders(w)
	c, err := h.Svc.RecordEmailOutcome(r.Context(), chi.URLParam(r, "token"), store.TriedOutcome(r.PostFormValue("outcome")))
	if ok, rerr := h.outcomeLinkError(w, r, err); ok {
		return rerr
	}
	if err != nil {
		return err
	}
	redirect(w, r, "/p/"+c.ProblemID.String()+"#s-"+c.SolutionID.String())
	return nil
}
