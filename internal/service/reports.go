package service

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
)

// Report target kinds (reports.target_kind).
const (
	ReportProblemRevision  = "problem_revision"
	ReportSolutionRevision = "solution_revision"
	ReportUser             = "user"
)

// CreateReport records a "flag this" from a signed-in user. The target must
// exist; the reason is 5..500 characters.
func (s *Service) CreateReport(ctx context.Context, reporterID uuid.UUID, kind, targetID, reason string) error {
	tid, err := uuid.Parse(strings.TrimSpace(targetID))
	if err != nil {
		return Invalid("target_id", "That isn't something that can be reported.")
	}
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n < 5 || n > 500 {
		return Invalid("reason", "Say what's wrong in 5 to 500 characters.")
	}
	var exists bool
	switch kind {
	case ReportProblemRevision:
		exists, err = s.Store.ReportProblemRevisionExists(ctx, tid)
	case ReportSolutionRevision:
		exists, err = s.Store.ReportSolutionRevisionExists(ctx, tid)
	case ReportUser:
		exists, err = s.Store.ReportUserExists(ctx, tid)
	default:
		return Invalid("target_kind", "That isn't something that can be reported.")
	}
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	return s.Store.CreateReport(ctx, store.CreateReportParams{
		ID: id, ReporterID: reporterID, TargetKind: kind, TargetID: tid, Reason: reason,
	})
}
