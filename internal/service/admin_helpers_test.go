package service_test

// Setup helpers for the reports/admin/seed tests. They use raw SQL so the
// tests don't depend on other domains' service code. Names are prefixed
// "mod" to stay clear of other test files in this package.

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// modSvc builds a Service over the package's test database with an
// insert-only River client (jobs land in river_job and are never worked).
func modSvc(t *testing.T) *service.Service {
	t.Helper()
	st := storetest.Store(t)
	rc, err := river.NewClient(riverpgxv5.New(st.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{RankHalfLife: 720 * time.Hour, RankMinVotesToTakeOver: 3, SoftSolvedThreshold: 5}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return service.New(st, rc, ranking.NewDecayScorer(cfg.RankHalfLife), service.NewWeigher(st, cfg), cfg, log)
}

func modExec(t *testing.T, s *service.Service, sql string, args ...any) {
	t.Helper()
	if _, err := s.Store.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func modID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// modUser inserts a user and returns (id, handle).
func modUser(t *testing.T, s *service.Service) (uuid.UUID, string) {
	t.Helper()
	id := modID(t)
	b := id[10:16]
	handle := "m_" + hex.EncodeToString(b[:])
	modExec(t, s, `INSERT INTO users (id, handle, display_name) VALUES ($1, $2, 'Mod Test')`, id, handle)
	return id, handle
}

// modProblem inserts a problem with one revision; returns (problemID, revisionID).
func modProblem(t *testing.T, s *service.Service, author uuid.UUID, display store.DisplayMode) (uuid.UUID, uuid.UUID) {
	t.Helper()
	pid, rid := modID(t), modID(t)
	modExec(t, s, `INSERT INTO problems (id, domain_id, author_id, author_display) VALUES ($1, 1, $2, $3)`, pid, author, display)
	modExec(t, s, `INSERT INTO problem_revisions (id, problem_id, author_id, author_display, title, current_process, pain, tried)
		VALUES ($1, $2, $3, $4, 'A test problem title', $5, 'It takes far too long every week.', 'nothing yet')`,
		rid, pid, author, display, strings.Repeat("Step one, then step two. ", 4))
	modExec(t, s, `UPDATE problems SET current_revision_id = $2 WHERE id = $1`, pid, rid)
	return pid, rid
}

// modSolution inserts a solution with one revision on problem; returns the revision id.
func modSolution(t *testing.T, s *service.Service, problem, author uuid.UUID) uuid.UUID {
	t.Helper()
	sid, rid := modID(t), modID(t)
	modExec(t, s, `INSERT INTO solutions (id, problem_id, author_id, author_display, kind) VALUES ($1, $2, $3, 'named', 'process_change')`, sid, problem, author)
	modExec(t, s, `INSERT INTO solution_revisions (id, solution_id, author_id, author_display, body)
		VALUES ($1, $2, $3, 'named', $4)`, rid, sid, author, strings.Repeat("Change the process like so. ", 3))
	modExec(t, s, `UPDATE solutions SET current_revision_id = $2 WHERE id = $1`, sid, rid)
	return rid
}
