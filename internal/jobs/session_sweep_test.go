package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

func TestSessionSweepDeletesOnlyExpired(t *testing.T) {
	ctx := context.Background()
	st := storetest.Store(t)
	uid := uuid.Must(uuid.NewV7())
	if _, err := st.CreateUser(ctx, store.CreateUserParams{ID: uid, Handle: "sweep_" + uid.String()[30:], DisplayName: "Sweep"}); err != nil {
		t.Fatal(err)
	}
	expired, live := []byte("expired-token-hash"), []byte("live-token-hash")
	for _, s := range []struct {
		hash []byte
		exp  time.Time
	}{{expired, time.Now().Add(-time.Minute)}, {live, time.Now().Add(time.Hour)}} {
		if err := st.CreateSession(ctx, store.CreateSessionParams{TokenHash: s.hash, UserID: uid, ExpiresAt: s.exp}); err != nil {
			t.Fatal(err)
		}
	}

	w := &SessionSweepWorker{d: Deps{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	if err := w.Work(ctx, &river.Job[SessionSweepArgs]{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSessionUser(ctx, live); err != nil {
		t.Fatalf("live session was deleted: %v", err)
	}
	// Expired sessions never resolve; check the row itself is gone by
	// sweeping again: nothing is left to delete.
	n, err := st.DeleteExpiredSessions(ctx)
	if err != nil || n != 0 {
		t.Fatalf("expired session survived the sweep: n=%d err=%v", n, err)
	}
	if _, err := st.GetSessionUser(ctx, expired); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired session: %v", err)
	}
}
