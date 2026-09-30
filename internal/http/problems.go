package http

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/views"
	"github.com/hi-naresh/unsolved/internal/views/pages"
	"github.com/hi-naresh/unsolved/internal/views/partials"
)

// displayCookie remembers the user's last posting-as choice.
const displayCookie = "us_display"

// mountProblems: front page, lists, the problem page and every problem write.
func (h *Handlers) mountProblems(r chi.Router) {
	r.Get("/", h.wrap(h.home))
	r.Get("/problems", h.wrap(h.problemsIndex))
	r.Get("/meta", h.wrap(h.metaBoard))
	r.Get("/p/{id}", h.wrap(h.problemPage))
	r.Get("/p/{id}/evolution", h.wrap(h.problemEvolution))

	r.With(auth.RequireWriter).Get("/new", h.wrap(h.newProblemForm))
	r.With(auth.RequireWriter, h.limit(ActNewProblem)).Post("/problems", h.wrap(h.createProblem))
	r.With(auth.RequireWriter).Get("/p/{id}/revise", h.wrap(h.reviseForm))
	r.With(auth.RequireWriter, h.limit(ActNewRevision)).Post("/p/{id}/revisions", h.wrap(h.createRevision))
	r.With(auth.RequireWriter).Post("/p/{id}/fork", h.wrap(h.forkProblem))
	r.With(auth.RequireWriter, h.limit(ActVote)).Post("/r/{revision_id}/vote", h.wrap(h.voteProblemRevision))
	r.With(auth.RequireWriter).Post("/p/{id}/state", h.wrap(h.setProblemState))
	r.With(auth.RequireWriter).Post("/p/{id}/interest", h.wrap(h.toggleInterest))
}

// --- lists ---

func (h *Handlers) home(w http.ResponseWriter, r *http.Request) error {
	after := r.URL.Query().Get("after")
	list, err := h.Svc.FrontPage(r.Context(), after)
	if err != nil {
		return err
	}
	rows := problemRowsView(list, "/?", "No open problems yet.")
	if isHX(r) && after != "" {
		return h.render(w, r, http.StatusOK, nil, partials.ProblemRows(rows))
	}
	v := pages.HomeView{List: rows}
	for _, p := range list.Pinned {
		row := problemRow(p)
		row.Pinned = true
		v.Pinned = append(v.Pinned, row)
	}
	return h.render(w, r, http.StatusOK, pages.Home(v), nil)
}

func (h *Handlers) problemsIndex(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	f := service.ProblemFilter{State: q.Get("state"), Domain: q.Get("domain"), Sort: q.Get("sort"), After: q.Get("after")}
	list, err := h.Svc.ListProblems(r.Context(), f)
	if err != nil {
		return err
	}
	base := url.Values{}
	for k, v := range map[string]string{"state": f.State, "domain": f.Domain, "sort": f.Sort} {
		if v != "" {
			base.Set(k, v)
		}
	}
	prefix := "/problems?"
	if len(base) > 0 {
		prefix += base.Encode() + "&"
	}
	rows := problemRowsView(list, prefix, "No problems match.")
	if isHX(r) && f.After != "" {
		return h.render(w, r, http.StatusOK, nil, partials.ProblemRows(rows))
	}
	domains, err := h.Svc.ProblemDomains(r.Context())
	if err != nil {
		return err
	}
	return h.render(w, r, http.StatusOK, pages.ProblemsIndex(pages.ProblemsIndexView{
		State: f.State, Domain: f.Domain, Sort: f.Sort, Domains: problemDomainOptions(domains), List: rows,
	}), nil)
}

func (h *Handlers) metaBoard(w http.ResponseWriter, r *http.Request) error {
	after := r.URL.Query().Get("after")
	list, err := h.Svc.MetaBoard(r.Context(), after)
	if err != nil {
		return err
	}
	rows := problemRowsView(list, "/meta?", "Nothing on the meta board yet.")
	if isHX(r) && after != "" {
		return h.render(w, r, http.StatusOK, nil, partials.ProblemRows(rows))
	}
	return h.render(w, r, http.StatusOK, pages.MetaBoard(rows), nil)
}

func problemRowsView(list service.ProblemList, prefix, empty string) partials.ProblemRowsView {
	v := partials.ProblemRowsView{Empty: empty}
	for _, p := range list.Items {
		v.Rows = append(v.Rows, problemRow(p))
	}
	if list.Next != "" {
		v.MoreURL = prefix + "after=" + url.QueryEscape(list.Next)
	}
	return v
}

func problemRow(p service.ProblemListItem) partials.ProblemRowView {
	return partials.ProblemRowView{
		ID: p.ID.String(), Title: p.Title, DomainName: p.DomainName, DomainSlug: p.DomainSlug,
		State: string(p.State), SoftSolved: p.SoftSolved, Votes: p.VoteCount, CreatedAt: p.CreatedAt,
		Author: contentAuthor(p.AuthorDisplay, p.AuthorHandle, p.AuthorDeleted, p.AuthorTier),
	}
}

func problemDomainOptions(ds []store.Domain) []pages.ProblemDomainOption {
	out := make([]pages.ProblemDomainOption, len(ds))
	for i, d := range ds {
		out[i] = pages.ProblemDomainOption{ID: strconv.Itoa(int(d.ID)), Slug: d.Slug, Name: d.Name}
	}
	return out
}

// --- posting ---

func (h *Handlers) newProblemForm(w http.ResponseWriter, r *http.Request) error {
	return h.renderNewProblem(w, r, http.StatusOK, pages.NewProblemForm{Display: contentDisplayDefault(r)})
}

func (h *Handlers) renderNewProblem(w http.ResponseWriter, r *http.Request, status int, f pages.NewProblemForm) error {
	domains, err := h.Svc.ProblemDomains(r.Context())
	if err != nil {
		return err
	}
	f.Domains = problemDomainOptions(domains)
	f.Handle = auth.UserFrom(r.Context()).Handle
	return h.render(w, r, status, pages.NewProblem(f), nil)
}

func (h *Handlers) createProblem(w http.ResponseWriter, r *http.Request) error {
	u := auth.UserFrom(r.Context())
	f := pages.NewProblemForm{
		DomainID:       r.PostFormValue("domain_id"),
		CurrentProcess: r.PostFormValue("current_process"),
		Pain:           r.PostFormValue("pain"),
		Tried:          r.PostFormValue("tried"),
		Title:          r.PostFormValue("title"),
		Display:        r.PostFormValue("display"),
	}
	domainID, _ := strconv.ParseInt(f.DomainID, 10, 16)
	id, err := h.Svc.CreateProblem(r.Context(), u.ID, service.NewProblem{
		DomainID: int16(domainID),
		ProblemFields: service.ProblemFields{
			Title: f.Title, CurrentProcess: f.CurrentProcess, Pain: f.Pain, Tried: f.Tried,
		},
		Display: store.DisplayMode(f.Display),
	})
	if errs := service.ValidationErrors(err); len(errs) > 0 {
		f.Errors = errs
		return h.renderNewProblem(w, r, http.StatusUnprocessableEntity, f)
	}
	if err != nil {
		return err
	}
	setContentDisplayCookie(w, h, f.Display)
	redirect(w, r, "/p/"+id.String())
	return nil
}

// --- the problem page ---

func (h *Handlers) problemPage(w http.ResponseWriter, r *http.Request) error {
	id, err := contentUUIDParam(r, "id")
	if err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	pg, err := h.Svc.ProblemPage(r.Context(), id, contentViewerID(u))
	if err != nil {
		return err
	}
	v := h.problemPageView(r, pg)
	return h.render(w, r, http.StatusOK, pages.Problem(v), pages.ProblemContent(v))
}

func (h *Handlers) problemPageView(r *http.Request, pg service.ProblemPage) pages.ProblemPageView {
	u := auth.UserFrom(r.Context())
	p := pg.Problem
	pid := p.ID.String()
	invalid := p.State == store.ProblemStateInvalid
	canWrite := u != nil && !u.Suspended && !invalid
	display := contentDisplayDefault(r)
	handle := ""
	if u != nil {
		handle = u.Handle
	}
	signIn := "/signin?next=" + url.QueryEscape("/p/"+pid)
	v := pages.ProblemPageView{
		ID: pid, RevisionID: p.RevisionID.String(), Title: p.Title,
		DomainName: p.DomainName, DomainSlug: p.DomainSlug,
		CurrentProcess: p.CurrentProcess, Pain: p.Pain, Tried: p.Tried,
		State: string(p.State), SoftSolved: p.SoftSolved, OnMetaBoard: p.OnMetaBoard, IsRevised: p.IsRevised,
		Poster:         contentAuthor(p.PosterDisplay, p.PosterHandle, p.PosterDeleted, p.PosterTier),
		RevisionAuthor: contentAuthor(p.RevisionAuthorDisplay, p.RevisionAuthorHandle, p.RevisionAuthorDeleted, p.RevisionAuthorTier),
		PostedAt:       partials.ProblemDate(p.CreatedAt),
		Vote:           voteView("/r/"+p.RevisionID.String()+"/vote", p.ViewerVoted, p.VoteCount, u, invalid, p.ViewerIsRevisionAuthor, signIn),
		Interest: partials.InterestView{
			ProblemID: pid, Count: p.InterestCount, Interested: p.ViewerInterested,
			Mode: interestMode(u, invalid), SignInURL: signIn,
		},
		SignedIn: u != nil, CanWrite: canWrite, ViewerIsPoster: p.ViewerIsPoster,
		SolutionForm: partials.SolutionFormView{ProblemID: pid, Handle: handle, Display: display},
		Community: pages.CommunityVoteView{
			SolvedPercent: pg.Community.SolvedPercent, InvalidPercent: pg.Community.InvalidPercent,
			VotedSolved: pg.Community.VotedSolved, VotedInvalid: pg.Community.VotedInvalid,
			CanVoteSolved: pg.Community.CanVoteSolved, CanVoteInvalid: pg.Community.CanVoteInvalid,
			SolvedOpensAt: partials.ProblemDate(pg.Community.SolvedOpensAt),
		},
	}
	if p.ForkedFromProblemID != nil {
		title := ""
		if p.ForkedFromTitle != nil {
			title = *p.ForkedFromTitle
		}
		v.ForkedFrom = &partials.ProblemLinkView{ID: p.ForkedFromProblemID.String(), Title: title}
	}
	for _, f := range pg.Forks {
		v.Forks = append(v.Forks, partials.ProblemLinkView{ID: f.ID.String(), Title: f.Title})
	}
	for _, s := range pg.Solutions {
		sv := partials.SolutionView{
			ID: s.ID.String(), ProblemID: pid, RevisionID: s.RevisionID.String(),
			Kind: string(s.Kind), Body: s.Body, IsRevised: s.IsRevised,
			Author: contentAuthor(s.AuthorDisplay, s.AuthorHandle, s.AuthorDeleted, s.AuthorTier),
			Vote:   voteView("/sr/"+s.RevisionID.String()+"/vote", s.ViewerVoted, s.VoteCount, u, invalid, s.ViewerIsAuthor, signIn),
			Worked: s.Worked, Partly: s.Partly, Failed: s.Failed, ViewerOutcome: s.ViewerOutcome,
			CanWrite: canWrite, Handle: handle, Display: display,
		}
		for _, n := range s.Notes {
			sv.Notes = append(sv.Notes, partials.TrialNoteView{Outcome: n.Outcome, Note: n.Note})
		}
		v.Solutions = append(v.Solutions, sv)
	}
	for _, e := range pg.History {
		v.History = append(v.History, pages.ProblemStateEventView{
			From: string(e.FromState), To: string(e.ToState), Reason: e.Reason,
			Actor: stateActorLabel(e.ActorKind), At: partials.ProblemDate(e.CreatedAt),
		})
	}
	return v
}

func stateActorLabel(kind string) string {
	switch kind {
	case "poster":
		return "the poster"
	case "moderator":
		return "a moderator"
	}
	return "the community"
}

// voteView builds a vote control for this viewer.
func voteView(postURL string, voted bool, count int32, u *auth.User, invalid, own bool, signIn string) partials.VoteButtonView {
	v := partials.VoteButtonView{URL: postURL, Voted: voted, Count: count, SignInURL: signIn}
	switch {
	case u == nil:
		v.Mode = partials.VoteSignIn
	case invalid:
		v.Mode, v.Note = partials.VoteStatic, "This problem is closed to votes"
	case own:
		v.Mode, v.Note = partials.VoteStatic, "You can't vote on your own revision"
	case u.Suspended:
		v.Mode, v.Note = partials.VoteStatic, "Your account is suspended"
	default:
		v.Mode = partials.VoteLive
	}
	return v
}

func interestMode(u *auth.User, invalid bool) partials.VoteMode {
	switch {
	case u == nil:
		return partials.VoteSignIn
	case invalid || u.Suspended:
		return partials.VoteStatic
	}
	return partials.VoteLive
}

// --- evolution and revisions ---

func (h *Handlers) problemEvolution(w http.ResponseWriter, r *http.Request) error {
	id, err := contentUUIDParam(r, "id")
	if err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	ev, err := h.Svc.Evolution(r.Context(), id, contentViewerID(u))
	if err != nil {
		return err
	}
	invalid := ev.Header.State == store.ProblemStateInvalid
	pid := id.String()
	signIn := "/signin?next=" + url.QueryEscape("/p/"+pid+"/evolution")
	v := pages.EvolutionView{
		ProblemID: pid, Title: ev.Header.Title, DomainName: ev.Header.DomainName,
		CanWrite: u != nil && !u.Suspended && !invalid,
	}
	for _, n := range ev.Revisions {
		why := ""
		if n.WhyNote != nil {
			why = *n.WhyNote
		}
		v.Revisions = append(v.Revisions, pages.EvolutionRevision{
			ID: n.ID.String(), Depth: n.Depth, Title: n.Title, CurrentProcess: n.CurrentProcess,
			Pain: n.Pain, Tried: n.Tried, WhyNote: why, IsCurrent: n.IsCurrent,
			Author:    contentAuthor(n.AuthorDisplay, n.AuthorHandle, n.AuthorDeleted, n.AuthorTier),
			Vote:      voteView("/r/"+n.ID.String()+"/vote", n.ViewerVoted, n.VoteCount, u, invalid, n.ViewerIsAuthor, signIn),
			CreatedAt: partials.ProblemDate(n.CreatedAt),
		})
	}
	return h.render(w, r, http.StatusOK, pages.Evolution(v), pages.EvolutionContent(v))
}

func (h *Handlers) reviseForm(w http.ResponseWriter, r *http.Request) error {
	id, err := contentUUIDParam(r, "id")
	if err != nil {
		return err
	}
	var from *uuid.UUID
	if s := r.URL.Query().Get("from"); s != "" {
		fid, err := uuid.Parse(s)
		if err != nil {
			return service.ErrNotFound
		}
		from = &fid
	}
	src, err := h.Svc.ReviseSource(r.Context(), id, from)
	if err != nil {
		return err
	}
	return h.render(w, r, http.StatusOK, pages.ReviseProblem(pages.ReviseProblemForm{
		ProblemID: id.String(), ParentRevisionID: src.RevisionID.String(), ParentIsCurrent: src.IsCurrent,
		DomainName: src.DomainName, CurrentProcess: src.CurrentProcess, Pain: src.Pain, Tried: src.Tried,
		Title: src.Title, Display: contentDisplayDefault(r), Handle: auth.UserFrom(r.Context()).Handle,
	}), nil)
}

func (h *Handlers) createRevision(w http.ResponseWriter, r *http.Request) error {
	id, err := contentUUIDParam(r, "id")
	if err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	f := pages.ReviseProblemForm{
		ProblemID:        id.String(),
		ParentRevisionID: r.PostFormValue("parent_revision_id"),
		CurrentProcess:   r.PostFormValue("current_process"),
		Pain:             r.PostFormValue("pain"),
		Tried:            r.PostFormValue("tried"),
		Title:            r.PostFormValue("title"),
		WhyNote:          r.PostFormValue("why_note"),
		Display:          r.PostFormValue("display"),
		Handle:           u.Handle,
	}
	parent, _ := uuid.Parse(f.ParentRevisionID) // a bad id fails the ownership check
	rid, err := h.Svc.CreateRevision(r.Context(), id, u.ID, service.NewRevision{
		ParentRevisionID: parent,
		ProblemFields: service.ProblemFields{
			Title: f.Title, CurrentProcess: f.CurrentProcess, Pain: f.Pain, Tried: f.Tried,
		},
		WhyNote: f.WhyNote,
		Display: store.DisplayMode(f.Display),
	})
	if errs := service.ValidationErrors(err); len(errs) > 0 {
		f.Errors = errs
		if src, serr := h.Svc.ReviseSource(r.Context(), id, nil); serr == nil {
			f.DomainName = src.DomainName
			f.ParentIsCurrent = src.RevisionID.String() == f.ParentRevisionID
		}
		return h.render(w, r, http.StatusUnprocessableEntity, pages.ReviseProblem(f), nil)
	}
	if err != nil {
		return err
	}
	setContentDisplayCookie(w, h, f.Display)
	redirect(w, r, "/p/"+id.String()+"/evolution#r-"+rid.String())
	return nil
}

func (h *Handlers) forkProblem(w http.ResponseWriter, r *http.Request) error {
	id, err := contentUUIDParam(r, "id")
	if err != nil {
		return err
	}
	rid, err := uuid.Parse(r.PostFormValue("revision_id"))
	if err != nil {
		return service.Invalid("revision_id", "Choose a version to fork.")
	}
	display := r.PostFormValue("display")
	if display == "" {
		display = contentDisplayDefault(r)
	}
	nid, err := h.Svc.Fork(r.Context(), id, rid, auth.UserFrom(r.Context()).ID, store.DisplayMode(display))
	if err != nil {
		return err
	}
	redirect(w, r, "/p/"+nid.String())
	return nil
}

// --- votes, state, interest ---

func (h *Handlers) voteProblemRevision(w http.ResponseWriter, r *http.Request) error {
	rid, err := contentUUIDParam(r, "revision_id")
	if err != nil {
		return err
	}
	u := auth.UserFrom(r.Context())
	res, err := h.Svc.ToggleProblemVote(r.Context(), rid, u.ID)
	if err != nil {
		return err
	}
	if !isHX(r) {
		redirect(w, r, "/p/"+res.ProblemID.String())
		return nil
	}
	v := partials.VoteButtonView{URL: "/r/" + rid.String() + "/vote", Voted: res.Voted, Count: res.Count, Mode: partials.VoteLive}
	return h.render(w, r, http.StatusOK, nil, partials.VoteButton(v))
}

func (h *Handlers) setProblemState(w http.ResponseWriter, r *http.Request) error {
	id, err := contentUUIDParam(r, "id")
	if err != nil {
		return err
	}
	to := store.ProblemState(r.PostFormValue("to"))
	// The poster marks solved / reopens; anyone else casts a community vote.
	if err := h.Svc.ChangeProblemState(r.Context(), id, auth.UserFrom(r.Context()).ID, to, r.PostFormValue("reason")); err != nil {
		return err
	}
	redirect(w, r, "/p/"+id.String()+"#state")
	return nil
}

func (h *Handlers) toggleInterest(w http.ResponseWriter, r *http.Request) error {
	id, err := contentUUIDParam(r, "id")
	if err != nil {
		return err
	}
	res, err := h.Svc.ToggleInterest(r.Context(), id, auth.UserFrom(r.Context()).ID)
	if err != nil {
		return err
	}
	if !isHX(r) {
		redirect(w, r, "/p/"+id.String())
		return nil
	}
	return h.render(w, r, http.StatusOK, nil, partials.ProblemInterest(partials.InterestView{
		ProblemID: id.String(), Count: res.Count, Interested: res.Interested, Mode: partials.VoteLive,
	}))
}

// --- helpers shared with solutions.go ---

// contentAuthor turns a row's authorship columns into the only thing a
// template may see. Anonymous rows lose their handle here.
func contentAuthor(display store.DisplayMode, handle string, deleted bool, tier string) views.AuthorRef {
	return views.NewAuthorRef(string(display), handle, deleted, tier)
}

func contentViewerID(u *auth.User) *uuid.UUID {
	if u == nil {
		return nil
	}
	id := u.ID
	return &id
}

// contentUUIDParam parses a UUID path parameter; a malformed id is a 404.
func contentUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, service.ErrNotFound
	}
	return id, nil
}

// contentDisplayDefault is the user's last posting-as choice.
func contentDisplayDefault(r *http.Request) string {
	if c, err := r.Cookie(displayCookie); err == nil && c.Value == string(store.DisplayModeAnonymous) {
		return string(store.DisplayModeAnonymous)
	}
	return string(store.DisplayModeNamed)
}

// setContentDisplayCookie remembers the posting-as choice after a successful post.
func setContentDisplayCookie(w http.ResponseWriter, h *Handlers, display string) {
	if display != string(store.DisplayModeAnonymous) {
		display = string(store.DisplayModeNamed)
	}
	http.SetCookie(w, &http.Cookie{
		Name: displayCookie, Value: display, Path: "/", MaxAge: int((365 * 24 * time.Hour).Seconds()),
		HttpOnly: true, Secure: h.Cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}
