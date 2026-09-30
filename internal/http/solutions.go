package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/views/pages"
	"github.com/hi-naresh/unsolved/internal/views/partials"
)

// mountSolutions: posting, revising, voting on and trying solutions. The
// forms live inline on the problem page.
func (h *Handlers) mountSolutions(r chi.Router) {
	r.With(auth.RequireWriter, h.limit(ActNewSolution)).Post("/p/{id}/solutions", h.wrap(h.createSolution))
	r.With(auth.RequireWriter, h.limit(ActNewRevision)).Post("/s/{id}/revisions", h.wrap(h.createSolutionRevision))
	r.With(auth.RequireWriter, h.limit(ActVote)).Post("/sr/{revision_id}/vote", h.wrap(h.voteSolutionRevision))
	r.With(auth.RequireWriter).Post("/s/{id}/tried", h.wrap(h.recordTrial))
}

func (h *Handlers) createSolution(w http.ResponseWriter, r *http.Request) error {
	pid, err := contentUUIDParam(r, "id")
	if err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	f := partials.SolutionFormView{
		ProblemID: pid.String(), Kind: r.PostFormValue("kind"), Body: r.PostFormValue("body"),
		Display: r.PostFormValue("display"), Handle: u.Handle,
	}
	sid, err := h.Svc.CreateSolution(r.Context(), pid, u.ID, service.NewSolution{
		Kind: store.SolutionKind(f.Kind), Body: f.Body, Display: store.DisplayMode(f.Display),
	})
	if errs := service.ValidationErrors(err); len(errs) > 0 {
		f.Errors = errs
		title := "Back to the problem"
		if pg, perr := h.Svc.ProblemPage(r.Context(), pid, nil); perr == nil {
			title = pg.Problem.Title
		}
		return h.render(w, r, http.StatusUnprocessableEntity, pages.SolutionFormPage(title, f), nil)
	}
	if err != nil {
		return err
	}
	setContentDisplayCookie(w, h, f.Display)
	redirect(w, r, "/p/"+pid.String()+"#s-"+sid.String())
	return nil
}

func (h *Handlers) createSolutionRevision(w http.ResponseWriter, r *http.Request) error {
	sid, err := contentUUIDParam(r, "id")
	if err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	f := partials.SolutionReviseFormView{
		SolutionID: sid.String(), ParentRevisionID: r.PostFormValue("parent_revision_id"),
		Body: r.PostFormValue("body"), WhyNote: r.PostFormValue("why_note"),
		Display: r.PostFormValue("display"), Handle: u.Handle,
	}
	parent, _ := uuid.Parse(f.ParentRevisionID) // a bad id fails the ownership check
	pid, err := h.Svc.CreateSolutionRevision(r.Context(), sid, u.ID, service.NewSolutionRevision{
		ParentRevisionID: parent, Body: f.Body, WhyNote: f.WhyNote, Display: store.DisplayMode(f.Display),
	})
	if errs := service.ValidationErrors(err); len(errs) > 0 {
		f.Errors = errs
		return h.render(w, r, http.StatusUnprocessableEntity, pages.SolutionRevisePage("", f), nil)
	}
	if err != nil {
		return err
	}
	setContentDisplayCookie(w, h, f.Display)
	redirect(w, r, "/p/"+pid.String()+"#s-"+sid.String())
	return nil
}

func (h *Handlers) voteSolutionRevision(w http.ResponseWriter, r *http.Request) error {
	rid, err := contentUUIDParam(r, "revision_id")
	if err != nil {
		return err
	}
	res, err := h.Svc.ToggleSolutionVote(r.Context(), rid, auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	if !isHX(r) {
		redirect(w, r, "/p/"+res.ProblemID.String()+"#solutions")
		return nil
	}
	v := partials.VoteButtonView{URL: "/sr/" + rid.String() + "/vote", Voted: res.Voted, Count: res.Count, Mode: partials.VoteLive}
	return h.render(w, r, http.StatusOK, nil, partials.VoteButton(v))
}

func (h *Handlers) recordTrial(w http.ResponseWriter, r *http.Request) error {
	sid, err := contentUUIDParam(r, "id")
	if err != nil {
		return err
	}
	pid, err := h.Svc.RecordTrial(r.Context(), sid, auth.UserFrom(r.Context()).ID,
		store.TriedOutcome(r.PostFormValue("outcome")), r.PostFormValue("note"))
	if err != nil {
		return err
	}
	redirect(w, r, "/p/"+pid.String()+"#s-"+sid.String())
	return nil
}
