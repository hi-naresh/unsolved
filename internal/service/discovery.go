package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/store"
)

// Phase 5 discovery: semantic search, the posting-form duplicate check,
// trending and admin merge suggestions.
//
// Search and Similar are the only places a request calls the ML service, each
// bounded by mlRequestTimeout and failing open. Page renders never call ML.

const (
	mlRequestTimeout   = 1500 * time.Millisecond
	searchLimit        = 20
	searchMinSim       = 0.2 // below this a "result" is noise
	searchMaxQueryLen  = 500 // runes
	similarLimit       = 5
	similarMinSim      = 0.6 // duplicate-check hint threshold
	similarMinInputLen = 40  // runes across the three fields before we bother
	mergePageSize      = 30
)

// DiscoveryItem is one problem in a search, similar or trending list. The
// author fields exist only to build a views.AuthorRef; AuthorHandle is blank
// for anonymous posts.
type DiscoveryItem struct {
	ProblemID     uuid.UUID
	Title         string
	DomainSlug    string
	DomainName    string
	State         store.ProblemState
	SoftSolved    bool
	AuthorDisplay store.DisplayMode
	AuthorHandle  string
	AuthorDeleted bool
	AuthorTier    string
	Similarity    float32 // 0 for trending
}

// SearchResult is what Search returns. Unavailable means the ML service is
// disabled, slow or failing: the caller shows a friendly note, not an error.
type SearchResult struct {
	Query       string
	Items       []DiscoveryItem
	Unavailable bool
}

// ml builds the ML client from config. Cheap: the connection pool lives in
// the jobs package's shared http.Client.
func (s *Service) ml() *jobs.MLClient { return jobs.NewMLClient(s.Cfg.MLURL) }

// embedQuery embeds one text within mlRequestTimeout.
func (s *Service) embedQuery(ctx context.Context, text string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, mlRequestTimeout)
	defer cancel()
	vecs, err := s.ml().Embed(ctx, []string{text})
	if err != nil {
		return "", err
	}
	return jobs.VectorLiteral(vecs[0]), nil
}

// Search returns up to 20 non-invalid problems whose current revision is
// nearest to q. An empty q returns an empty result.
func (s *Service) Search(ctx context.Context, q string) (SearchResult, error) {
	q = truncateRunes(strings.TrimSpace(q), searchMaxQueryLen)
	res := SearchResult{Query: q}
	if q == "" {
		return res, nil
	}
	vec, err := s.embedQuery(ctx, q)
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err() // the request itself went away
		}
		s.logMLFailure(ctx, "search", err)
		res.Unavailable = true
		return res, nil
	}
	rows, err := s.Store.SearchProblemsByEmbedding(ctx, store.SearchProblemsByEmbeddingParams{Embedding: vec, Lim: searchLimit})
	if err != nil {
		return res, err
	}
	for _, r := range rows {
		if r.Similarity < searchMinSim {
			continue
		}
		res.Items = append(res.Items, discoveryItem(r.ProblemID, r.Title, r.DomainSlug, r.DomainName, r.State, r.SoftSolved,
			r.AuthorDisplay, r.AuthorHandle, r.AuthorDeleted, r.AuthorTier, r.Similarity))
	}
	return res, nil
}

// Similar is the posting form's duplicate check: up to 5 existing problems
// close to what is being typed. It fails open: any ML or input problem gives
// an empty list and no error.
func (s *Service) Similar(ctx context.Context, title, currentProcess, pain string) ([]DiscoveryItem, error) {
	title = truncateRunes(strings.TrimSpace(title), 140)
	currentProcess = truncateRunes(strings.TrimSpace(currentProcess), 4000)
	pain = truncateRunes(strings.TrimSpace(pain), 2000)
	if utf8.RuneCountInString(title+currentProcess+pain) < similarMinInputLen {
		return nil, nil
	}
	vec, err := s.embedQuery(ctx, jobs.RevisionText(title, currentProcess, pain))
	if err != nil {
		if ctx.Err() == nil {
			s.logMLFailure(ctx, "similar", err)
		}
		return nil, nil
	}
	rows, err := s.Store.SearchProblemsByEmbedding(ctx, store.SearchProblemsByEmbeddingParams{Embedding: vec, Lim: similarLimit})
	if err != nil {
		return nil, err
	}
	var out []DiscoveryItem
	for _, r := range rows {
		if r.Similarity < similarMinSim {
			continue
		}
		out = append(out, discoveryItem(r.ProblemID, r.Title, r.DomainSlug, r.DomainName, r.State, r.SoftSolved,
			r.AuthorDisplay, r.AuthorHandle, r.AuthorDeleted, r.AuthorTier, r.Similarity))
	}
	return out, nil
}

// Trending returns the list ClusterNightly last wrote, best first.
func (s *Service) Trending(ctx context.Context) ([]DiscoveryItem, error) {
	rows, err := s.Store.ListTrending(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]DiscoveryItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, discoveryItem(r.ProblemID, r.Title, r.DomainSlug, r.DomainName, r.State, r.SoftSolved,
			r.AuthorDisplay, r.AuthorHandle, r.AuthorDeleted, r.AuthorTier, 0))
	}
	return out, nil
}

func discoveryItem(id uuid.UUID, title, slug, name string, state store.ProblemState, soft bool,
	display store.DisplayMode, handle string, deleted bool, tier string, sim float32) DiscoveryItem {
	if display == store.DisplayModeAnonymous {
		handle = "" // never let an anonymous author's handle leave the service
	}
	return DiscoveryItem{
		ProblemID: id, Title: title, DomainSlug: slug, DomainName: name, State: state, SoftSolved: soft,
		AuthorDisplay: display, AuthorHandle: handle, AuthorDeleted: deleted, AuthorTier: tier, Similarity: sim,
	}
}

func (s *Service) logMLFailure(ctx context.Context, what string, err error) {
	if errors.Is(err, jobs.ErrMLDisabled) {
		return
	}
	s.Log.WarnContext(ctx, "ml unavailable; failing open", "call", what, "err", err)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// MergeSuggestion is one open admin merge suggestion.
type MergeSuggestion struct {
	ID         uuid.UUID
	Similarity float32
	ProblemA   uuid.UUID
	TitleA     string
	ProblemB   uuid.UUID
	TitleB     string
}

// MergeSuggestionsPage is one keyset page; Next is the ?after= cursor for the
// following page, empty on the last.
type MergeSuggestionsPage struct {
	Items []MergeSuggestion
	Next  string
}

// MergeSuggestions lists open suggestions, most similar first, 30 per page.
// after is the opaque cursor "<similarity>_<id>" from the previous page.
func (s *Service) MergeSuggestions(ctx context.Context, after string) (MergeSuggestionsPage, error) {
	var page MergeSuggestionsPage
	arg := store.ListMergeSuggestionsParams{AfterSimilarity: 2, AfterID: uuid.Max, Lim: mergePageSize + 1}
	if after != "" {
		sim, id, err := parseMergeCursor(after)
		if err != nil {
			return page, Invalid("after", "Bad page cursor.")
		}
		arg.AfterSimilarity, arg.AfterID = sim, id
	}
	rows, err := s.Store.ListMergeSuggestions(ctx, arg)
	if err != nil {
		return page, err
	}
	if len(rows) > mergePageSize {
		rows = rows[:mergePageSize]
		last := rows[len(rows)-1]
		page.Next = strconv.FormatFloat(float64(last.Similarity), 'g', -1, 32) + "_" + last.ID.String()
	}
	for _, r := range rows {
		page.Items = append(page.Items, MergeSuggestion{
			ID: r.ID, Similarity: r.Similarity, ProblemA: r.ProblemAID, TitleA: r.TitleA, ProblemB: r.ProblemBID, TitleB: r.TitleB,
		})
	}
	return page, nil
}

func parseMergeCursor(c string) (float32, uuid.UUID, error) {
	simStr, idStr, ok := strings.Cut(c, "_")
	if !ok {
		return 0, uuid.Nil, fmt.Errorf("cursor: no separator")
	}
	f, err := strconv.ParseFloat(simStr, 32)
	if err != nil {
		return 0, uuid.Nil, err
	}
	id, err := uuid.Parse(idStr)
	return float32(f), id, err
}

// DismissMergeSuggestion hides a suggestion for good; the nightly job never
// brings it back. Admin action: logged at WARN with the admin's id.
func (s *Service) DismissMergeSuggestion(ctx context.Context, adminID, id uuid.UUID) error {
	n, err := s.Store.DismissMergeSuggestion(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	s.Log.WarnContext(ctx, "admin dismissed merge suggestion", "admin_id", adminID, "merge_suggestion_id", id)
	return nil
}
