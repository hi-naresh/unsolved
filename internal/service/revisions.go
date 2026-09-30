package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// Revision rules:
//   - A new revision's parent is the revision the author was viewing; it must
//     belong to the same problem.
//   - why_note is required (1..500) for every non-first revision.
//   - Revisions are immutable: there is no edit or delete.

// NewRevision is the revise form.
type NewRevision struct {
	ParentRevisionID uuid.UUID
	ProblemFields
	WhyNote string
	Display store.DisplayMode
}

// CreateRevision adds a child revision to a problem and enqueues
// EmbedRevision in the same transaction. It does not change the current
// revision: votes do that, through PickCurrentRevision.
func (s *Service) CreateRevision(ctx context.Context, problemID, authorID uuid.UUID, in NewRevision) (uuid.UUID, error) {
	var errs []error
	in.ProblemFields = cleanProblemFields(in.ProblemFields, &errs)
	in.WhyNote = contentText(in.WhyNote)
	contentLen(&errs, "why_note", "The note on why you changed it", in.WhyNote, 1, whyNoteMax)
	display, err := contentDisplay(in.Display)
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return uuid.Nil, errors.Join(errs...)
	}
	p, err := s.Store.GetProblemForWrite(ctx, problemID)
	if err != nil {
		return uuid.Nil, notFound(err)
	}
	if p.State == store.ProblemStateInvalid {
		return uuid.Nil, ErrForbidden
	}
	parent, err := s.Store.GetProblemRevisionText(ctx, in.ParentRevisionID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	if err != nil || parent.ProblemID != problemID {
		return uuid.Nil, Invalid("parent_revision_id", "That revision doesn't belong to this problem.")
	}
	rid, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	why := in.WhyNote
	err = s.Store.InTx(ctx, func(q *store.Queries, tx pgx.Tx) error {
		if err := q.InsertProblemRevision(ctx, store.InsertProblemRevisionParams{
			ID: rid, ProblemID: problemID, ParentRevisionID: &in.ParentRevisionID,
			AuthorID: authorID, AuthorDisplay: display,
			Title: in.Title, CurrentProcess: in.CurrentProcess, Pain: in.Pain, Tried: in.Tried,
			WhyNote: &why, CreatedAt: s.Now(),
		}); err != nil {
			return err
		}
		return s.enqueueContentJob(ctx, tx, jobs.EmbedRevisionArgs{RevisionID: rid}, nil)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return rid, nil
}

// ReviseSource is the revision a revise form is prefilled from: from, or the
// current revision when from is nil.
func (s *Service) ReviseSource(ctx context.Context, problemID uuid.UUID, from *uuid.UUID) (store.GetReviseSourceRow, error) {
	r, err := s.Store.GetReviseSource(ctx, store.GetReviseSourceParams{ProblemID: problemID, RevisionID: from})
	if err != nil {
		return r, notFound(err)
	}
	if r.State == store.ProblemStateInvalid {
		return r, ErrForbidden
	}
	return r, nil
}

// RevisionNode is one revision in the evolution tree, in depth-first order.
type RevisionNode struct {
	store.ListProblemRevisionsForTreeRow
	Depth     int
	IsCurrent bool
}

// Evolution is the "How this evolved" page.
type Evolution struct {
	Header    store.GetEvolutionHeaderRow
	Revisions []RevisionNode
}

// Evolution reads every revision of a problem (one flat query) and orders
// them as a tree by walking parent_revision_id.
func (s *Service) Evolution(ctx context.Context, problemID uuid.UUID, viewer *uuid.UUID) (Evolution, error) {
	var ev Evolution
	h, err := s.Store.GetEvolutionHeader(ctx, problemID)
	if err != nil {
		return ev, notFound(err)
	}
	ev.Header = h
	rows, err := s.Store.ListProblemRevisionsForTree(ctx, store.ListProblemRevisionsForTreeParams{ProblemID: problemID, ViewerID: viewer})
	if err != nil {
		return ev, err
	}
	var current uuid.UUID
	if h.CurrentRevisionID != nil {
		current = *h.CurrentRevisionID
	}
	ev.Revisions = revisionTree(rows, current)
	return ev, nil
}

// revisionTree orders rows (sorted by created_at) depth-first, children in
// creation order. A row whose parent is missing is treated as a root.
func revisionTree(rows []store.ListProblemRevisionsForTreeRow, current uuid.UUID) []RevisionNode {
	known := make(map[uuid.UUID]bool, len(rows))
	for _, r := range rows {
		known[r.ID] = true
	}
	children := map[uuid.UUID][]int{}
	var roots []int
	for i, r := range rows {
		if r.ParentRevisionID == nil || !known[*r.ParentRevisionID] {
			roots = append(roots, i)
			continue
		}
		children[*r.ParentRevisionID] = append(children[*r.ParentRevisionID], i)
	}
	out := make([]RevisionNode, 0, len(rows))
	type item struct{ idx, depth int }
	stack := make([]item, 0, len(rows))
	for i := len(roots) - 1; i >= 0; i-- {
		stack = append(stack, item{roots[i], 0})
	}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		r := rows[it.idx]
		out = append(out, RevisionNode{ListProblemRevisionsForTreeRow: r, Depth: it.depth, IsCurrent: r.ID == current})
		kids := children[r.ID]
		for i := len(kids) - 1; i >= 0; i-- {
			stack = append(stack, item{kids[i], it.depth + 1})
		}
	}
	return out
}
