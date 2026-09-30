package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
	"github.com/jackc/pgx/v5"
)

func testService(t *testing.T) *Service {
	t.Helper()
	st := storetest.Store(t)
	cfg := config.Config{SessionTTL: time.Hour}
	return New(st, nil, nil, nil, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

var seq atomic.Int64

func newTestUser(t *testing.T, s *Service) store.User {
	t.Helper()
	n := seq.Add(1)
	u, created, err := s.SignInWithIdentity(context.Background(), store.ProviderX,
		fmt.Sprintf("svc-%d-%d", time.Now().UnixNano(), n), "https://x.com/u", fmt.Sprintf("User %d", n))
	if err != nil || !created {
		t.Fatalf("new user: created=%v err=%v", created, err)
	}
	return u
}

func uniqueHandle(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, seq.Add(1))
}

// seedProblem writes a problem with one revision by author. It uses COPY in
// one transaction (no SQL outside sqlc files; the deferred
// current_revision_id FK is checked at commit).
func seedProblem(t *testing.T, s *Service, author uuid.UUID, display, title string) (problemID, revisionID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	problemID, revisionID = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	err := pgx.BeginFunc(ctx, s.Store.Pool, func(tx pgx.Tx) error {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"problems"},
			[]string{"id", "domain_id", "author_id", "author_display", "current_revision_id"},
			pgx.CopyFromRows([][]any{{problemID, int16(1), author, display, revisionID}})); err != nil {
			return err
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"problem_revisions"},
			[]string{"id", "problem_id", "author_id", "author_display", "title", "current_process", "pain", "tried"},
			pgx.CopyFromRows([][]any{{revisionID, problemID, author, display, title,
				strings.Repeat("Step by step we do the thing. ", 3), "It takes far too long every week.", "nothing yet"}}))
		return err
	})
	if err != nil {
		t.Fatalf("seed problem: %v", err)
	}
	return problemID, revisionID
}

// seedSolution writes a solution with one revision on problemID.
func seedSolution(t *testing.T, s *Service, problemID, author uuid.UUID, display string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	solID, revID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	err := pgx.BeginFunc(ctx, s.Store.Pool, func(tx pgx.Tx) error {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"solutions"},
			[]string{"id", "problem_id", "author_id", "author_display", "kind", "current_revision_id"},
			pgx.CopyFromRows([][]any{{solID, problemID, author, display, "process_change", revID}})); err != nil {
			return err
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"solution_revisions"},
			[]string{"id", "solution_id", "author_id", "author_display", "body"},
			pgx.CopyFromRows([][]any{{revID, solID, author, display, strings.Repeat("Use a shared checklist instead. ", 3)}}))
		return err
	})
	if err != nil {
		t.Fatalf("seed solution: %v", err)
	}
	return solID
}

func TestValidateHandle(t *testing.T) {
	tests := []struct {
		handle string
		ok     bool
	}{
		{"alice", true},
		{"a_b_9", true},
		{"abc", true},
		{strings.Repeat("a", 24), true},
		{"ab", false},
		{strings.Repeat("a", 25), false},
		{"Alice", false}, // ValidateHandle expects normalized input
		{"al-ice", false},
		{"al ice", false},
		{"émile", false},
		{"admin", false},
		{"unsolved", false},
		{"deleted_1234abcd", false},
		{"deleted_", false},
	}
	for _, tc := range tests {
		err := ValidateHandle(tc.handle)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateHandle(%q) = %v, want ok=%v", tc.handle, err, tc.ok)
		}
		var ve ErrValidation
		if err != nil && (!errors.As(err, &ve) || ve.Field != "handle") {
			t.Errorf("ValidateHandle(%q): want ErrValidation{Field: handle}, got %v", tc.handle, err)
		}
	}
}

func TestCompleteWelcome(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	taken := newTestUser(t, s)
	if err := s.CompleteWelcome(ctx, taken.ID, uniqueHandle("taken"), false); err != nil {
		t.Fatal(err)
	}
	taken, _ = s.GetUser(ctx, taken.ID)

	tests := []struct {
		name      string
		handle    string
		directory bool
		wantField string // "" = success
	}{
		{"valid, opted in", uniqueHandle("newbie"), true, ""},
		{"normalized", "  " + strings.ToUpper(uniqueHandle("shout")) + " ", false, ""},
		{"too short", "ab", false, "handle"},
		{"hyphen", "a-b-c", false, "handle"},
		{"reserved", "admin", false, "handle"},
		{"deleted prefix", "deleted_abcdef12", false, "handle"},
		{"taken", taken.Handle, false, "handle"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := newTestUser(t, s)
			err := s.CompleteWelcome(ctx, u.ID, tc.handle, tc.directory)
			if tc.wantField == "" {
				if err != nil {
					t.Fatal(err)
				}
				got, _ := s.GetUser(ctx, u.ID)
				if got.Handle != NormalizeHandle(tc.handle) || got.InDirectory != tc.directory {
					t.Fatalf("got handle=%q dir=%v", got.Handle, got.InDirectory)
				}
				return
			}
			var ve ErrValidation
			if !errors.As(err, &ve) || ve.Field != tc.wantField {
				t.Fatalf("want ErrValidation{%s}, got %v", tc.wantField, err)
			}
		})
	}
}

func TestUpdateSettings(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	tests := []struct {
		name      string
		in        func(u store.User) Settings
		wantField string
		check     func(t *testing.T, u store.User)
	}{
		{
			name: "all fields",
			in: func(u store.User) Settings {
				return Settings{Handle: uniqueHandle("full"), InDirectory: true, DeclaredHistory: "  Ran a warehouse for 10 years. ", Email: "me@example.com"}
			},
			check: func(t *testing.T, u store.User) {
				if !u.InDirectory || u.DeclaredHistory == nil || *u.DeclaredHistory != "Ran a warehouse for 10 years." || u.Email == nil || *u.Email != "me@example.com" {
					t.Fatalf("not saved: %+v", u)
				}
			},
		},
		{
			name: "empty clears",
			in:   func(u store.User) Settings { return Settings{Handle: u.Handle} },
			check: func(t *testing.T, u store.User) {
				if u.DeclaredHistory != nil || u.Email != nil || u.InDirectory {
					t.Fatalf("not cleared: %+v", u)
				}
			},
		},
		{
			name: "history at limit",
			in: func(u store.User) Settings {
				return Settings{Handle: u.Handle, DeclaredHistory: strings.Repeat("é", 2000)}
			},
			check: func(t *testing.T, u store.User) {},
		},
		{name: "history too long", in: func(u store.User) Settings {
			return Settings{Handle: u.Handle, DeclaredHistory: strings.Repeat("a", 2001)}
		}, wantField: "declared_history"},
		{name: "bad email", in: func(u store.User) Settings { return Settings{Handle: u.Handle, Email: "not-an-email"} }, wantField: "email"},
		{name: "email with name", in: func(u store.User) Settings { return Settings{Handle: u.Handle, Email: "Bob <bob@example.com>"} }, wantField: "email"},
		{name: "email no dot", in: func(u store.User) Settings { return Settings{Handle: u.Handle, Email: "bob@localhost"} }, wantField: "email"},
		{name: "email too long", in: func(u store.User) Settings {
			return Settings{Handle: u.Handle, Email: strings.Repeat("a", 250) + "@example.com"}
		}, wantField: "email"},
		{name: "bad handle", in: func(u store.User) Settings { return Settings{Handle: "no"} }, wantField: "handle"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := newTestUser(t, s)
			if err := s.UpdateSettings(ctx, u.ID, Settings{Handle: u.Handle, InDirectory: true, DeclaredHistory: "x", Email: "old@example.com"}); err != nil {
				t.Fatal(err)
			}
			err := s.UpdateSettings(ctx, u.ID, tc.in(u))
			if tc.wantField != "" {
				var ve ErrValidation
				if !errors.As(err, &ve) || ve.Field != tc.wantField {
					t.Fatalf("want ErrValidation{%s}, got %v", tc.wantField, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := s.GetUser(ctx, u.ID)
			tc.check(t, got)
		})
	}
}

func TestProfileByHandle(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	u := newTestUser(t, s)
	other := newTestUser(t, s)
	namedP, _ := seedProblem(t, s, u.ID, "named", "Named problem title here")
	seedProblem(t, s, u.ID, "anonymous", "Secret anonymous problem")
	seedSolution(t, s, namedP, u.ID, "anonymous")
	otherP, _ := seedProblem(t, s, other.ID, "named", "Someone else's problem")
	seedSolution(t, s, otherP, u.ID, "named")

	p, err := s.ProfileByHandle(ctx, u.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Contributions) != 2 {
		t.Fatalf("want 2 named contributions, got %+v", p.Contributions)
	}
	for _, c := range p.Contributions {
		if c.Title == "Secret anonymous problem" {
			t.Fatal("anonymous problem on profile")
		}
		if c.Kind == "solution" && c.ProblemID != otherP {
			t.Fatal("anonymous solution on profile")
		}
	}
	if len(p.Links) != 0 {
		t.Fatal("links shown while not in directory")
	}

	if err := s.UpdateSettings(ctx, u.ID, Settings{Handle: u.Handle, InDirectory: true}); err != nil {
		t.Fatal(err)
	}
	p, _ = s.ProfileByHandle(ctx, strings.ToUpper(u.Handle))
	if len(p.Links) != 1 || p.Links[0].URL != "https://x.com/u" {
		t.Fatalf("links: %+v", p.Links)
	}

	if _, err := s.ProfileByHandle(ctx, "nobody_here"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if err := s.DeleteAccount(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProfileByHandle(ctx, DeletedHandle(u.ID, false)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

func TestDirectoryPagination(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	var want []string
	for i := 0; i < 32; i++ {
		u := newTestUser(t, s)
		h := uniqueHandle("dir")
		if err := s.CompleteWelcome(ctx, u.ID, h, true); err != nil {
			t.Fatal(err)
		}
		want = append(want, h)
	}
	hidden := newTestUser(t, s)
	_ = s.CompleteWelcome(ctx, hidden.ID, uniqueHandle("hidden"), false)
	gone := newTestUser(t, s)
	_ = s.CompleteWelcome(ctx, gone.ID, uniqueHandle("gone"), true)
	_ = s.DeleteAccount(ctx, gone.ID)

	var got []string
	after := ""
	for pages := 0; ; pages++ {
		ms, next, err := s.Directory(ctx, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(ms) > 30 {
			t.Fatalf("page of %d", len(ms))
		}
		for _, m := range ms {
			got = append(got, m.Handle)
		}
		if next == "" {
			break
		}
		after = next
		if pages > 100 {
			t.Fatal("pagination does not terminate")
		}
	}
	seen := map[string]bool{}
	for _, h := range got {
		if seen[h] {
			t.Fatalf("duplicate %s", h)
		}
		seen[h] = true
		if strings.HasPrefix(h, "hidden_") || strings.HasPrefix(h, "gone_") || strings.HasPrefix(h, "deleted_") {
			t.Fatalf("%s should not be listed", h)
		}
	}
	for _, h := range want {
		if !seen[h] {
			t.Fatalf("%s missing from directory", h)
		}
	}
	if _, _, err := s.Directory(ctx, "garbage"); err == nil {
		t.Fatal("bad cursor accepted")
	}
}

func TestExportAndDelete(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	u := newTestUser(t, s)
	_ = s.UpdateSettings(ctx, u.ID, Settings{Handle: u.Handle, Email: "me@example.com", DeclaredHistory: "history"})
	pid, _ := seedProblem(t, s, u.ID, "anonymous", "My anonymous problem")

	raw, err := s.ExportUserData(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ex struct {
		User struct {
			Handle string `json:"handle"`
			Email  string `json:"email"`
		} `json:"user"`
		Identities []struct {
			Provider    string `json:"provider"`
			ProviderUID string `json:"provider_uid"`
		} `json:"identities"`
		Problems []struct {
			ID            uuid.UUID `json:"id"`
			AuthorDisplay string    `json:"author_display"`
		} `json:"problems"`
		ProblemRevisions []struct {
			Title string `json:"title"`
		} `json:"problem_revisions"`
		Votes []json.RawMessage `json:"problem_revision_votes"`
	}
	if err := json.Unmarshal(raw, &ex); err != nil {
		t.Fatalf("export is not JSON: %v\n%s", err, raw)
	}
	if ex.User.Handle != u.Handle || ex.User.Email != "me@example.com" || len(ex.Identities) != 1 ||
		len(ex.Problems) != 1 || ex.Problems[0].ID != pid || ex.Problems[0].AuthorDisplay != "anonymous" ||
		len(ex.ProblemRevisions) != 1 || ex.ProblemRevisions[0].Title != "My anonymous problem" || ex.Votes == nil {
		t.Fatalf("export incomplete: %s", raw)
	}

	token, _, _ := s.NewSession(ctx, u.ID)
	if err := s.DeleteAccount(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Store.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Handle != DeletedHandle(u.ID, false) || got.DisplayName != "deleted user" || got.Email != nil ||
		got.DeclaredHistory != nil || got.InDirectory || got.DeletedAt == nil {
		t.Fatalf("not scrubbed: %+v", got)
	}
	if !handleRE.MatchString(got.Handle) {
		t.Fatalf("scrubbed handle %q violates the handle rule", got.Handle)
	}
	if _, err := s.SessionUser(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatal("session survived deletion")
	}
	if ids, _ := s.Store.ListIdentitiesByUser(ctx, u.ID); len(ids) != 0 {
		t.Fatal("identities survived deletion")
	}
	if _, err := s.ExportUserData(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("export after delete: %v", err)
	}
	if err := s.DeleteAccount(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestDeletedHandle(t *testing.T) {
	a := uuid.MustParse("01929a1b-2c3d-7e4f-8a9b-0c1d2e3f4a5b")
	if got := DeletedHandle(a, false); got != "deleted_2e3f4a5b" {
		t.Fatalf("got %q", got)
	}
	if got := DeletedHandle(a, true); got != "deleted_8a9b0c1d2e3f4a5b" || len(got) > 24 {
		t.Fatalf("got %q", got)
	}
}

func TestAdminHandlesCannotChangeHandsBySelfService(t *testing.T) {
	ctx := context.Background()
	svc := testService(t)
	svc.Cfg.AdminHandles = []string{"boss_handle"}
	u, _, err := svc.SignInWithIdentity(ctx, store.ProviderX, "admin-guard-"+uuid.NewString(), "", "A")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CompleteWelcome(ctx, u.ID, "boss_handle", false); err == nil {
		t.Fatal("claimed an admin handle on /welcome")
	}
	if err := svc.UpdateSettings(ctx, u.ID, Settings{Handle: "boss_handle"}); err == nil {
		t.Fatal("claimed an admin handle in settings")
	}
	if err := svc.AssignAdminHandle(ctx, u.Handle, "boss_handle"); err != nil {
		t.Fatalf("operator assign: %v", err)
	}
	if err := svc.UpdateSettings(ctx, u.ID, Settings{Handle: "someone_else"}); err == nil {
		t.Fatal("renamed away from an admin handle")
	}
	if err := svc.UpdateSettings(ctx, u.ID, Settings{Handle: "boss_handle", InDirectory: true}); err != nil {
		t.Fatalf("keeping the admin handle should be fine: %v", err)
	}
	if err := svc.AssignAdminHandle(ctx, u.Handle, "not_listed"); err == nil {
		t.Fatal("assigned a handle not in ADMIN_HANDLES")
	}
}
