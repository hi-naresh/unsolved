package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/views"
	"github.com/hi-naresh/unsolved/internal/views/pages"
	"github.com/hi-naresh/unsolved/internal/views/partials"
)

// mountDiscovery: phase 5 semantic search, duplicate check and admin merge
// suggestions.
func (h *Handlers) mountDiscovery(r chi.Router) {
	r.Get("/search", h.wrap(h.search))
	r.With(auth.RequireUser, h.limit(ActVote)).Post("/new/similar", h.wrap(h.similar))
	r.With(auth.RequireAdmin).Get("/admin/merges", h.wrap(h.adminMerges))
	r.With(auth.RequireAdmin).Post("/admin/merges/{id}/dismiss", h.wrap(h.dismissMerge))
}

// search: GET /search?q=. Empty q shows trending; with q, semantic results.
// If ML is unavailable the page says so and shows trending instead.
func (h *Handlers) search(w http.ResponseWriter, r *http.Request) error {
	res, err := h.Svc.Search(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		return err
	}
	v := partials.SearchView{Query: res.Query, Unavailable: res.Unavailable, Results: problemLinks(res.Items)}
	if res.Query == "" || res.Unavailable {
		trending, err := h.Svc.Trending(r.Context())
		if err != nil {
			return err
		}
		v.Trending = problemLinks(trending)
	}
	return h.render(w, r, http.StatusOK, pages.Search(v), partials.DiscoverySearchResults(v))
}

// similar: POST /new/similar from the posting form (htmx, after typing).
// Returns the hint fragment, or an empty body when nothing is close or ML is
// down, so the form never breaks.
func (h *Handlers) similar(w http.ResponseWriter, r *http.Request) error {
	items, err := h.Svc.Similar(r.Context(),
		r.PostFormValue("title"), r.PostFormValue("current_process"), r.PostFormValue("pain"))
	if err != nil {
		// Fail open here too: the duplicate check is a hint, never a blocker.
		h.Log.WarnContext(r.Context(), "duplicate check failed", "err", err)
		items = nil
	}
	return h.render(w, r, http.StatusOK, partials.DiscoverySimilar(problemLinks(items)), nil)
}

func (h *Handlers) adminMerges(w http.ResponseWriter, r *http.Request) error {
	page, err := h.Svc.MergeSuggestions(r.Context(), r.URL.Query().Get("after"))
	if err != nil {
		return err
	}
	rows := make([]partials.MergeRow, 0, len(page.Items))
	for _, m := range page.Items {
		rows = append(rows, partials.MergeRow{
			ID: m.ID.String(), Similarity: m.Similarity,
			ProblemA: m.ProblemA.String(), TitleA: m.TitleA,
			ProblemB: m.ProblemB.String(), TitleB: m.TitleB,
		})
	}
	return h.render(w, r, http.StatusOK, pages.AdminMerges(rows, page.Next), partials.DiscoveryMerges(rows, page.Next))
}

func (h *Handlers) dismissMerge(w http.ResponseWriter, r *http.Request) error {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return service.ErrNotFound
	}
	if err := h.Svc.DismissMergeSuggestion(r.Context(), auth.UserFrom(r.Context()).ID, id); err != nil {
		return err
	}
	if isHX(r) {
		// The row's outerHTML is swapped for nothing.
		w.WriteHeader(http.StatusOK)
		return nil
	}
	http.Redirect(w, r, "/admin/merges", http.StatusSeeOther)
	return nil
}

func problemLinks(items []service.DiscoveryItem) []partials.ProblemLink {
	out := make([]partials.ProblemLink, 0, len(items))
	for _, it := range items {
		out = append(out, partials.ProblemLink{
			ProblemID:  it.ProblemID.String(),
			Title:      it.Title,
			Domain:     it.DomainName,
			Solved:     it.State == store.ProblemStateSolved,
			Similarity: it.Similarity,
			Author:     views.NewAuthorRef(string(it.AuthorDisplay), it.AuthorHandle, it.AuthorDeleted, it.AuthorTier),
		})
	}
	return out
}
