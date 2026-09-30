package service

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// Every admin action below either writes a problem_state_events row (state
// changes) or a WARN log line carrying the admin's id, as BUILD.md requires.

// AdminPageSize is the /admin report list page size.
const AdminPageSize = 30

// ListOpenReports returns unresolved reports oldest first, keyset-paginated
// by after ("<created_at unix µs>_<id>", empty for the first page). next is
// the cursor for the following page, empty when there is none.
func (s *Service) ListOpenReports(ctx context.Context, after string) (rows []store.AdminListOpenReportsRow, next string, err error) {
	p := store.AdminListOpenReportsParams{Lim: AdminPageSize + 1}
	if after != "" {
		ts, id, ok := parseReportCursor(after)
		if !ok {
			return nil, "", Invalid("after", "Bad page cursor.")
		}
		p.AfterCreated, p.AfterID = &ts, &id
	}
	rows, err = s.Store.AdminListOpenReports(ctx, p)
	if err != nil {
		return nil, "", err
	}
	if len(rows) > AdminPageSize {
		rows = rows[:AdminPageSize]
		last := rows[len(rows)-1]
		next = strconv.FormatInt(last.CreatedAt.UnixMicro(), 10) + "_" + last.ID.String()
	}
	return rows, next, nil
}

func parseReportCursor(c string) (time.Time, uuid.UUID, bool) {
	a, b, ok := strings.Cut(c, "_")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	us, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	id, err := uuid.Parse(b)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return time.UnixMicro(us).UTC(), id, true
}

// ResolveReport marks a report handled.
func (s *Service) ResolveReport(ctx context.Context, adminID, reportID uuid.UUID) error {
	n, err := s.Store.AdminResolveReport(ctx, reportID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	s.Log.WarnContext(ctx, "admin action", "admin_id", adminID, "action", "resolve_report", "report_id", reportID)
	return nil
}

// AdminMarkInvalid sets a problem's state to invalid and records a
// problem_state_events row (actor = admin), in one transaction.
func (s *Service) AdminMarkInvalid(ctx context.Context, adminID, problemID uuid.UUID, reason string) error {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 1 || n > 500 {
		return Invalid("reason", "Give a reason (up to 500 characters).")
	}
	return s.adminSetState(ctx, adminID, problemID, store.ProblemStateInvalid, reason)
}

// AdminReopen moves an invalid problem back to open, with an event row.
func (s *Service) AdminReopen(ctx context.Context, adminID, problemID uuid.UUID, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Reopened by an admin."
	}
	if utf8.RuneCountInString(reason) > 500 {
		return Invalid("reason", "Keep the reason under 500 characters.")
	}
	return s.adminSetState(ctx, adminID, problemID, store.ProblemStateOpen, reason)
}

func (s *Service) adminSetState(ctx context.Context, adminID, problemID uuid.UUID, to store.ProblemState, reason string) error {
	eventID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	return s.Store.InTx(ctx, func(q *store.Queries, tx pgx.Tx) error {
		lp, err := q.LockProblemForStateVote(ctx, problemID)
		if err != nil {
			return notFound(err)
		}
		from := lp.State
		switch {
		case from == to:
			return ErrConflict
		case to == store.ProblemStateOpen && from != store.ProblemStateInvalid:
			// Admins only reopen what they (or the community) invalidated;
			// solved → open is the poster's call.
			return ErrConflict
		}
		if err := q.AdminSetProblemState(ctx, store.AdminSetProblemStateParams{ID: problemID, State: to}); err != nil {
			return err
		}
		if err := q.AdminInsertStateEvent(ctx, store.AdminInsertStateEventParams{
			ID: eventID, ProblemID: problemID, FromState: from, ToState: to, ActorID: &adminID, Reason: reason,
		}); err != nil {
			return err
		}
		// Leaving or returning from invalid moves the poster's (and top
		// solution author's) standing points.
		return s.enqueueStandingForStateChange(ctx, q, tx, problemID, lp.AuthorID, lp.DomainID)
	})
}

// AdminToggleMeta moves a problem onto or off the meta board.
func (s *Service) AdminToggleMeta(ctx context.Context, adminID, problemID uuid.UUID) (bool, error) {
	on, err := s.Store.AdminToggleMetaBoard(ctx, problemID)
	if err != nil {
		return false, notFound(err)
	}
	s.Log.WarnContext(ctx, "admin action", "admin_id", adminID, "action", "toggle_meta", "problem_id", problemID, "on_meta_board", on)
	return on, nil
}

// AdminSetSuspended suspends (blocks all writes) or unsuspends a user.
func (s *Service) AdminSetSuspended(ctx context.Context, adminID uuid.UUID, handle string, suspend bool) error {
	u, err := s.Store.GetUserByHandle(ctx, handle)
	if err != nil {
		return notFound(err)
	}
	action := "unsuspend"
	if suspend {
		action = "suspend"
		_, err = s.Store.AdminSuspendUser(ctx, u.ID)
	} else {
		_, err = s.Store.AdminUnsuspendUser(ctx, u.ID)
	}
	if err != nil {
		return err
	}
	s.Log.WarnContext(ctx, "admin action", "admin_id", adminID, "action", action, "target_user_id", u.ID)
	return nil
}

// AdminZeroVotes sets weight = 0 on every vote the user has cast (problem and
// solution revisions, and community state votes) and, in the same
// transaction, enqueues RescoreAll for the affected problems. It returns the number of votes zeroed and the
// affected problem ids (sorted, deduplicated).
func (s *Service) AdminZeroVotes(ctx context.Context, adminID uuid.UUID, handle string) (int, []uuid.UUID, error) {
	u, err := s.Store.GetUserByHandle(ctx, handle)
	if err != nil {
		return 0, nil, notFound(err)
	}
	var (
		count int
		ids   []uuid.UUID
	)
	err = s.Store.InTx(ctx, func(q *store.Queries, tx pgx.Tx) error {
		pids, err := q.AdminZeroProblemVoteWeights(ctx, u.ID)
		if err != nil {
			return err
		}
		sids, err := q.AdminZeroSolutionVoteWeights(ctx, u.ID)
		if err != nil {
			return err
		}
		nstate, err := q.AdminZeroStateVoteWeights(ctx, u.ID)
		if err != nil {
			return err
		}
		count = len(pids) + len(sids) + int(nstate)
		ids = append(append(ids, pids...), sids...)
		slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
		ids = slices.Compact(ids)
		if len(ids) == 0 {
			return nil // nothing to rescore; an empty list would mean "everything"
		}
		_, err = s.Jobs.InsertTx(ctx, tx, jobs.RescoreAllArgs{ProblemIDs: ids}, nil)
		return err
	})
	if err != nil {
		return 0, nil, err
	}
	s.Log.WarnContext(ctx, "admin action", "admin_id", adminID, "action", "zero_votes",
		"target_user_id", u.ID, "votes_zeroed", count, "problems", len(ids))
	return count, ids, nil
}

// AdminRescoreAll enqueues a full RescoreAll (after a half-life or weight change).
func (s *Service) AdminRescoreAll(ctx context.Context, adminID uuid.UUID) error {
	if _, err := s.Jobs.Insert(ctx, jobs.RescoreAllArgs{}, nil); err != nil {
		return err
	}
	s.Log.WarnContext(ctx, "admin action", "admin_id", adminID, "action", "rescore_all")
	return nil
}
