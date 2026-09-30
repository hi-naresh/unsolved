package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
)

// ProblemPageSize is the keyset page size of every problem list.
const ProblemPageSize = 30

// seedPinLimit: seed problems stay pinned on the front page until this many
// non-seed problems exist.
const seedPinLimit = 50

// Text limits, identical to the CHECK constraints in 0001_init.sql.
const (
	titleMin, titleMax     = 10, 140
	processMin, processMax = 50, 4000
	painMin, painMax       = 20, 2000
	triedMax               = 2000
	whyNoteMax             = 500
	solutionBodyMin        = 50
	solutionBodyMax        = 8000
	trialNoteMax           = 1000
	stateReasonMax         = 500
)

// ProblemFields is the text of one problem revision.
type ProblemFields struct {
	Title          string
	CurrentProcess string
	Pain           string
	Tried          string
}

// NewProblem is the guided posting form.
type NewProblem struct {
	DomainID int16
	ProblemFields
	Display store.DisplayMode
	IsSeed  bool // set only by cmd/seed
}

// CreateProblem inserts the problem, its first revision and points
// current_revision_id at it, in one transaction, and enqueues EmbedRevision
// in the same transaction.
func (s *Service) CreateProblem(ctx context.Context, authorID uuid.UUID, in NewProblem) (uuid.UUID, error) {
	var errs []error
	if in.DomainID <= 0 {
		errs = append(errs, Invalid("domain_id", "Choose a domain."))
	}
	in.ProblemFields = cleanProblemFields(in.ProblemFields, &errs)
	display, err := contentDisplay(in.Display)
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return uuid.Nil, errors.Join(errs...)
	}
	return s.insertProblem(ctx, authorID, in.DomainID, display, in.ProblemFields, nil, in.IsSeed)
}

func (s *Service) insertProblem(ctx context.Context, authorID uuid.UUID, domainID int16, display store.DisplayMode,
	f ProblemFields, forkedFrom *uuid.UUID, isSeed bool) (uuid.UUID, error) {
	pid, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	rid, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	now := s.Now()
	err = s.Store.InTx(ctx, func(q *store.Queries, tx pgx.Tx) error {
		if err := q.InsertProblem(ctx, store.InsertProblemParams{
			ID: pid, DomainID: domainID, AuthorID: authorID, AuthorDisplay: display,
			CurrentRevisionID: &rid, ForkedFromRevisionID: forkedFrom, IsSeed: isSeed, Now: now,
		}); err != nil {
			return err
		}
		if err := q.InsertProblemRevision(ctx, store.InsertProblemRevisionParams{
			ID: rid, ProblemID: pid, AuthorID: authorID, AuthorDisplay: display,
			Title: f.Title, CurrentProcess: f.CurrentProcess, Pain: f.Pain, Tried: f.Tried, CreatedAt: now,
		}); err != nil {
			return err
		}
		return s.enqueueContentJob(ctx, tx, jobs.EmbedRevisionArgs{RevisionID: rid})
	})
	if isFKViolation(err, "problems_domain_id_fkey") {
		return uuid.Nil, Invalid("domain_id", "Choose a domain.")
	}
	if err != nil {
		return uuid.Nil, err
	}
	return pid, nil
}

// Fork creates a new problem whose first revision is a copy of revisionID's
// text. The new problem records forked_from_revision_id; votes are not copied.
func (s *Service) Fork(ctx context.Context, problemID, revisionID, userID uuid.UUID, display store.DisplayMode) (uuid.UUID, error) {
	d, err := contentDisplay(display)
	if err != nil {
		return uuid.Nil, err
	}
	rev, err := s.Store.GetProblemRevisionText(ctx, revisionID)
	if err != nil {
		return uuid.Nil, notFound(err)
	}
	if rev.ProblemID != problemID {
		return uuid.Nil, ErrNotFound
	}
	p, err := s.Store.GetProblemForWrite(ctx, problemID)
	if err != nil {
		return uuid.Nil, notFound(err)
	}
	if p.State == store.ProblemStateInvalid {
		return uuid.Nil, ErrForbidden
	}
	f := ProblemFields{Title: rev.Title, CurrentProcess: rev.CurrentProcess, Pain: rev.Pain, Tried: rev.Tried}
	return s.insertProblem(ctx, userID, p.DomainID, d, f, &revisionID, false)
}

// ProblemDomains lists the domains for the posting form.
func (s *Service) ProblemDomains(ctx context.Context) ([]store.Domain, error) {
	return s.Store.ListDomains(ctx)
}

// ProblemLink is a problem id and title (fork links).
type ProblemLink struct {
	ID    uuid.UUID `json:"id"`
	Title string    `json:"title"`
}

// TrialNote is one recent "tried it" note. Trials carry no author.
type TrialNote struct {
	Outcome string `json:"outcome"`
	Note    string `json:"note"`
}

// SolutionItem is a solution on the problem page.
type SolutionItem struct {
	store.ListProblemSolutionsRow
	Notes []TrialNote
}

// ProblemPage is everything /p/{id} shows, read in three queries.
type ProblemPage struct {
	Problem   store.GetProblemPageRow
	Forks     []ProblemLink
	Solutions []SolutionItem
	History   []store.ListProblemStateHistoryRow
}

// ProblemPage reads the problem page. viewer may be nil (signed out).
func (s *Service) ProblemPage(ctx context.Context, id uuid.UUID, viewer *uuid.UUID) (ProblemPage, error) {
	var pg ProblemPage
	p, err := s.Store.GetProblemPage(ctx, store.GetProblemPageParams{ID: id, ViewerID: viewer})
	if err != nil {
		return pg, notFound(err)
	}
	pg.Problem = p
	if err := json.Unmarshal(p.Forks, &pg.Forks); err != nil {
		return pg, fmt.Errorf("decode forks: %w", err)
	}
	sols, err := s.Store.ListProblemSolutions(ctx, store.ListProblemSolutionsParams{ProblemID: id, ViewerID: viewer})
	if err != nil {
		return pg, err
	}
	for _, r := range sols {
		it := SolutionItem{ListProblemSolutionsRow: r}
		if err := json.Unmarshal(r.RecentNotes, &it.Notes); err != nil {
			return pg, fmt.Errorf("decode trial notes: %w", err)
		}
		pg.Solutions = append(pg.Solutions, it)
	}
	pg.History, err = s.Store.ListProblemStateHistory(ctx, id)
	return pg, err
}

// ProblemListItem is one row of any problem list.
type ProblemListItem = store.ListFrontPageRow

// ProblemList is one keyset page. Next is the ?after= value for the next
// page, empty on the last page. Pinned holds seed problems (front page, first
// page only).
type ProblemList struct {
	Pinned []ProblemListItem
	Items  []ProblemListItem
	Next   string
}

// FrontPage lists open, not soft-solved problems by score. Seed problems are
// pinned on top of the first page until 50 non-seed problems exist.
func (s *Service) FrontPage(ctx context.Context, after string) (ProblemList, error) {
	var out ProblemList
	c, err := parseScoreCursor(after)
	if err != nil {
		return out, err
	}
	n, err := s.Store.CountNonSeedProblemsUpTo50(ctx)
	if err != nil {
		return out, err
	}
	pin := n < seedPinLimit
	if pin && !c.set {
		seeds, err := s.Store.ListPinnedSeedProblems(ctx)
		if err != nil {
			return out, err
		}
		for _, r := range seeds {
			out.Pinned = append(out.Pinned, ProblemListItem(r))
		}
	}
	rows, err := s.Store.ListFrontPage(ctx, store.ListFrontPageParams{
		ExcludeSeeds: pin, HasAfter: c.set, AfterScore: c.score, AfterID: c.id, Lim: ProblemPageSize + 1,
	})
	if err != nil {
		return out, err
	}
	out.Items, out.Next = pageScore(rows)
	return out, nil
}

// ProblemFilter is the /problems query string.
type ProblemFilter struct {
	State  string // open|solved|invalid, empty = any
	Domain string // domain slug, empty = any
	Sort   string // top (default) | new
	After  string
}

// ListProblems is the filtered /problems list.
func (s *Service) ListProblems(ctx context.Context, f ProblemFilter) (ProblemList, error) {
	var out ProblemList
	var state *store.ProblemState
	if f.State != "" {
		st := store.ProblemState(f.State)
		if !st.Valid() {
			return out, Invalid("state", "Unknown state.")
		}
		state = &st
	}
	var domain *string
	if f.Domain != "" {
		domain = &f.Domain
	}
	switch f.Sort {
	case "", "top":
		c, err := parseScoreCursor(f.After)
		if err != nil {
			return out, err
		}
		rows, err := s.Store.ListProblemsTop(ctx, store.ListProblemsTopParams{
			State: state, DomainSlug: domain, HasAfter: c.set, AfterScore: c.score, AfterID: c.id, Lim: ProblemPageSize + 1,
		})
		if err != nil {
			return out, err
		}
		items := make([]ProblemListItem, len(rows))
		for i, r := range rows {
			items[i] = ProblemListItem(r)
		}
		out.Items, out.Next = pageScore(items)
	case "new":
		var at time.Time
		var id uuid.UUID
		set := f.After != ""
		if set {
			var err error
			if at, id, err = parseTimeCursor(f.After); err != nil {
				return out, err
			}
		}
		rows, err := s.Store.ListProblemsNew(ctx, store.ListProblemsNewParams{
			State: state, DomainSlug: domain, HasAfter: set, AfterCreatedAt: at, AfterID: id, Lim: ProblemPageSize + 1,
		})
		if err != nil {
			return out, err
		}
		items := make([]ProblemListItem, len(rows))
		for i, r := range rows {
			items[i] = ProblemListItem(r)
		}
		if len(items) > ProblemPageSize {
			items = items[:ProblemPageSize]
			last := items[len(items)-1]
			out.Next = strconv.FormatInt(last.CreatedAt.UnixNano(), 10) + "_" + last.ID.String()
		}
		out.Items = items
	default:
		return out, Invalid("sort", "Unknown sort.")
	}
	return out, nil
}

// MetaBoard lists problems on the meta board by score.
func (s *Service) MetaBoard(ctx context.Context, after string) (ProblemList, error) {
	var out ProblemList
	c, err := parseScoreCursor(after)
	if err != nil {
		return out, err
	}
	rows, err := s.Store.ListMetaBoard(ctx, store.ListMetaBoardParams{
		HasAfter: c.set, AfterScore: c.score, AfterID: c.id, Lim: ProblemPageSize + 1,
	})
	if err != nil {
		return out, err
	}
	items := make([]ProblemListItem, len(rows))
	for i, r := range rows {
		items[i] = ProblemListItem(r)
	}
	out.Items, out.Next = pageScore(items)
	return out, nil
}

// pageScore trims a LIMIT page+1 result and builds the next score cursor.
func pageScore(rows []ProblemListItem) ([]ProblemListItem, string) {
	if len(rows) <= ProblemPageSize {
		return rows, ""
	}
	rows = rows[:ProblemPageSize]
	last := rows[len(rows)-1]
	return rows, strconv.FormatFloat(last.Score, 'g', -1, 64) + "_" + last.ID.String()
}

type scoreCursor struct {
	set   bool
	score float64
	id    uuid.UUID
}

// parseScoreCursor reads ?after=<score>_<id>. Empty means the first page.
func parseScoreCursor(s string) (scoreCursor, error) {
	if s == "" {
		return scoreCursor{}, nil
	}
	a, b, ok := strings.Cut(s, "_")
	if !ok {
		return scoreCursor{}, Invalid("after", "Bad page cursor.")
	}
	score, err := strconv.ParseFloat(a, 64)
	if err != nil {
		return scoreCursor{}, Invalid("after", "Bad page cursor.")
	}
	id, err := uuid.Parse(b)
	if err != nil {
		return scoreCursor{}, Invalid("after", "Bad page cursor.")
	}
	return scoreCursor{set: true, score: score, id: id}, nil
}

// parseTimeCursor reads ?after=<created_at unix nano>_<id>.
func parseTimeCursor(s string) (time.Time, uuid.UUID, error) {
	a, b, ok := strings.Cut(s, "_")
	if !ok {
		return time.Time{}, uuid.Nil, Invalid("after", "Bad page cursor.")
	}
	n, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, Invalid("after", "Bad page cursor.")
	}
	id, err := uuid.Parse(b)
	if err != nil {
		return time.Time{}, uuid.Nil, Invalid("after", "Bad page cursor.")
	}
	return time.Unix(0, n).UTC(), id, nil
}

// --- shared content helpers (problems, revisions, solutions) ---

// cleanProblemFields normalises and validates revision text, appending one
// ErrValidation per bad field.
func cleanProblemFields(f ProblemFields, errs *[]error) ProblemFields {
	f.Title = contentText(f.Title)
	f.CurrentProcess = contentText(f.CurrentProcess)
	f.Pain = contentText(f.Pain)
	f.Tried = contentText(f.Tried)
	contentLen(errs, "current_process", "The step-by-step description", f.CurrentProcess, processMin, processMax)
	contentLen(errs, "pain", "What goes wrong", f.Pain, painMin, painMax)
	contentLen(errs, "tried", "What you've tried", f.Tried, 0, triedMax)
	contentLen(errs, "title", "The title", f.Title, titleMin, titleMax)
	return f
}

// contentText trims and normalises line endings (browsers send CRLF).
func contentText(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}

// contentLen checks a length in characters, as Postgres length() counts.
func contentLen(errs *[]error, field, label, v string, min, max int) {
	if strings.ContainsRune(v, 0) || !utf8.ValidString(v) {
		*errs = append(*errs, Invalid(field, label+" contains characters we can't store."))
		return
	}
	n := utf8.RuneCountInString(v)
	switch {
	case min > 0 && n == 0:
		*errs = append(*errs, Invalid(field, label+" is required."))
	case n < min:
		*errs = append(*errs, Invalid(field, fmt.Sprintf("%s needs at least %d characters (it has %d).", label, min, n)))
	case n > max:
		*errs = append(*errs, Invalid(field, fmt.Sprintf("%s can be at most %d characters (it has %d).", label, max, n)))
	}
}

// contentDisplay validates a per-post display mode; empty means named.
func contentDisplay(d store.DisplayMode) (store.DisplayMode, error) {
	if d == "" {
		return store.DisplayModeNamed, nil
	}
	if !d.Valid() {
		return "", Invalid("display", "Choose named or anonymous.")
	}
	return d, nil
}

// enqueueContentJob inserts a job in the caller's transaction.
func (s *Service) enqueueContentJob(ctx context.Context, tx pgx.Tx, args river.JobArgs) error {
	if s.Jobs == nil {
		return errors.New("service: no job client configured")
	}
	_, err := s.Jobs.InsertTx(ctx, tx, args, nil)
	return err
}

func isFKViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503" && pe.ConstraintName == constraint
}

// ValidationErrors flattens an error from a service call into its field
// errors (a joined error may hold several). Handlers use it to re-render a
// form with one message per field.
func ValidationErrors(err error) map[string]string {
	out := map[string]string{}
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		var ve ErrValidation
		if v, ok := e.(ErrValidation); ok {
			if _, dup := out[v.Field]; !dup {
				out[v.Field] = v.Msg
			}
			return
		}
		if m, ok := e.(interface{ Unwrap() []error }); ok {
			for _, x := range m.Unwrap() {
				walk(x)
			}
			return
		}
		if errors.As(e, &ve) {
			if _, dup := out[ve.Field]; !dup {
				out[ve.Field] = ve.Msg
			}
		}
	}
	walk(err)
	return out
}
