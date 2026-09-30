package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// Standing tiers that count as proven in a domain: they may vouch and vote
// a problem invalid there.
func provenTier(tier string) bool {
	return tier == string(store.StandingTierDomainContributor) || tier == string(store.StandingTierDomainExpert)
}

// enqueueStanding enqueues RecomputeStanding for (userID, domainID) in tx.
func (s *Service) enqueueStanding(ctx context.Context, tx pgx.Tx, userID uuid.UUID, domainID int16) error {
	if s.Jobs == nil {
		return nil // only in tests that run without a job client
	}
	return jobs.EnqueueStanding(ctx, s.Jobs, tx, userID, domainID)
}

// enqueueStandingForStateChange: a problem's state change moves the poster's
// "solved" points and the top solution author's points.
func (s *Service) enqueueStandingForStateChange(ctx context.Context, q *store.Queries, tx pgx.Tx, problemID, posterID uuid.UUID, domainID int16) error {
	if err := s.enqueueStanding(ctx, tx, posterID, domainID); err != nil {
		return err
	}
	top, err := q.GetTopSolutionAuthor(ctx, problemID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if top.AuthorID == posterID {
		return nil
	}
	return s.enqueueStanding(ctx, tx, top.AuthorID, domainID)
}
