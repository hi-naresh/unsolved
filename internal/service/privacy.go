package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// ExportUserData returns the user's own data as JSON (UK GDPR access
// request): profile, identities, every problem, solution and revision they
// wrote including anonymous ones, votes, trials, founder interest, reports
// filed and vouches given. Built by one query, so it is a consistent snapshot.
func (s *Service) ExportUserData(ctx context.Context, userID uuid.UUID) (json.RawMessage, error) {
	if _, err := s.GetUser(ctx, userID); err != nil {
		return nil, err
	}
	data, err := s.Store.ExportUserData(ctx, userID)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// DeletedHandle is the scrubbed handle of a deleted account: "deleted_" plus
// 8 hex characters of the user id. UUIDv7 ids start with a millisecond
// timestamp, so the leading hex digits of users created close together are
// identical; the trailing (random) ones are used instead. wide uses 16 hex
// characters, for the vanishingly rare collision.
func DeletedHandle(id uuid.UUID, wide bool) string {
	h := strings.ReplaceAll(id.String(), "-", "")
	if wide {
		return "deleted_" + h[len(h)-16:]
	}
	return "deleted_" + h[len(h)-8:]
}

// DeleteAccount deletes identities and sessions and scrubs the user row in
// one transaction. Content stays, shown as "deleted user". The same provider
// account signing in later gets a fresh, unrelated user.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID) error {
	del := func(wide bool) error {
		return s.Store.InTx(ctx, func(q *store.Queries, _ pgx.Tx) error {
			if err := q.DeleteUserIdentities(ctx, userID); err != nil {
				return err
			}
			if err := q.DeleteUserSessions(ctx, userID); err != nil {
				return err
			}
			n, err := q.ScrubUser(ctx, store.ScrubUserParams{ID: userID, Handle: DeletedHandle(userID, wide)})
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrNotFound
			}
			return nil
		})
	}
	err := del(false)
	if handleTaken(err) {
		err = del(true)
	}
	if err == nil {
		s.Log.InfoContext(ctx, "account deleted")
	}
	return err
}
