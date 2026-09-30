package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
)

// p4Env is a contentEnv whose service uses the real NewWeigher with rw.
func p4Env(t *testing.T, rw config.RankWeights, rwJSON string) *contentEnv {
	t.Helper()
	e := newContentEnv(t)
	cfg := config.Config{RankHalfLife: 720 * time.Hour, RankMinVotesToTakeOver: 3, SoftSolvedThreshold: 5,
		RankWeightsJSON: rwJSON, RankWeights: rw}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	scorer := ranking.NewDecayScorer(cfg.RankHalfLife)
	e.svc = service.New(e.st, e.rc, scorer, service.NewWeigher(e.st, cfg), cfg, log)
	e.deps = jobs.Deps{Store: e.st, Scorer: scorer, Cfg: cfg, Log: log}
	return e
}

func (e *contentEnv) setTier(t *testing.T, user uuid.UUID, domain int16, tier string) {
	t.Helper()
	if _, err := e.st.Pool.Exec(context.Background(), `
		INSERT INTO user_domain_standing (user_id, domain_id, tier) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, domain_id) DO UPDATE SET tier = EXCLUDED.tier`, user, domain, tier); err != nil {
		t.Fatal(err)
	}
}

func (e *contentEnv) standing(t *testing.T, user uuid.UUID, domain int16) (string, float64) {
	t.Helper()
	var tier string
	var pts float64
	err := e.st.Pool.QueryRow(context.Background(),
		`SELECT tier::text, points FROM user_domain_standing WHERE user_id = $1 AND domain_id = $2`, user, domain).Scan(&tier, &pts)
	if err != nil {
		return "none", 0
	}
	return tier, pts
}

func (e *contentEnv) p4exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.st.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func TestTierWeigherReadsStanding(t *testing.T) {
	rw := config.RankWeights{Tiers: map[string]float64{"domain_contributor": 2, "domain_expert": 4}, SuspiciousFactor: 0.5, NewAccountHours: 24}
	e := p4Env(t, rw, "{}")
	ctx := context.Background()
	member, expert, youngExpert, suspended := e.user(t), e.user(t), e.user(t), e.user(t)
	for _, u := range []uuid.UUID{member, expert, suspended} {
		e.p4exec(t, `UPDATE users SET created_at = now() - interval '30 days' WHERE id = $1`, u)
	}
	e.setTier(t, expert, 1, "domain_expert")
	e.setTier(t, youngExpert, 1, "domain_expert")
	e.setTier(t, suspended, 1, "domain_contributor")
	e.p4exec(t, `UPDATE users SET suspended_at = now() WHERE id = $1`, suspended)
	cases := []struct {
		name   string
		user   uuid.UUID
		domain int16
		want   float64
	}{
		{"member", member, 1, 1},
		{"expert in domain", expert, 1, 4},
		{"expert elsewhere is member", expert, 2, 1},
		{"young account", youngExpert, 1, 2},
		{"suspended", suspended, 1, 1},
	}
	for _, tc := range cases {
		got, err := e.svc.Weigher.Weight(ctx, tc.user, tc.domain)
		if err != nil || got != tc.want {
			t.Errorf("%s: weight %v err %v, want %v", tc.name, got, err, tc.want)
		}
	}
	// The vote snapshot uses it.
	pid, rid := e.problem(t, member, "Tier-weighted votes on route planning")
	if _, err := e.svc.ToggleProblemVote(ctx, rid, expert); err != nil {
		t.Fatal(err)
	}
	var w float32
	if err := e.st.Pool.QueryRow(ctx, `SELECT weight FROM problem_revision_votes WHERE revision_id = $1 AND user_id = $2`, rid, expert).Scan(&w); err != nil || w != 4 {
		t.Fatalf("vote weight %v err %v (problem %s)", w, err, pid)
	}
}

func TestRecomputeStanding(t *testing.T) {
	e := p4Env(t, config.RankWeights{TierPoints: map[string]float64{"domain_contributor": 15, "domain_expert": 40}}, "{}")
	ctx := context.Background()
	a, b, c, voucher := e.user(t), e.user(t), e.user(t), e.user(t)
	run := func(u uuid.UUID) {
		t.Helper()
		if err := jobs.RecomputeStanding(ctx, e.deps, &u, 0); err != nil {
			t.Fatal(err)
		}
	}

	// No activity: no row. A first revision with no votes earns nothing.
	run(a)
	if tier, _ := e.standing(t, a, 1); tier != "none" {
		t.Fatalf("no activity should write no row, got %s", tier)
	}
	p1, r1 := e.problem(t, a, "Standing: quoting jobs from paper drawings")
	run(a)
	if tier, pts := e.standing(t, a, 1); tier != "member" || pts != 0 {
		t.Fatalf("unvoted first revision: %s %v", tier, pts)
	}

	// One vote from someone else: current first revision +2.
	if _, err := e.svc.ToggleProblemVote(ctx, r1, b); err != nil {
		t.Fatal(err)
	}
	run(a)
	if tier, pts := e.standing(t, a, 1); tier != "member" || pts != 2 {
		t.Fatalf("after one vote: %s %v", tier, pts)
	}

	// An anonymous problem marked solved: +10, and it still counts.
	anon := sampleProblem("Standing: anonymous solved problem about invoices")
	anon.Display = store.DisplayModeAnonymous
	p2, err := e.svc.CreateProblem(ctx, a, anon)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetProblemState(ctx, p2, a, store.ProblemStateSolved, ""); err != nil {
		t.Fatal(err)
	}
	if n := e.jobCount(t, "recompute_standing", "user_id", a.String()); n < 1 {
		t.Fatal("marking solved did not enqueue RecomputeStanding for the poster")
	}
	run(a)
	if tier, pts := e.standing(t, a, 1); tier != "member" || pts != 12 {
		t.Fatalf("after solved: %s %v", tier, pts)
	}

	// A's revision of C's problem becomes current: +5; votes on it count
	// towards +1 per 3.
	p3, r3 := e.problem(t, c, "Standing: C's problem that A improves")
	f := sampleProblem("").ProblemFields
	f.Title = "Standing: C's problem, sharper wording by A"
	rev, err := e.svc.CreateRevision(ctx, p3, a, service.NewRevision{ParentRevisionID: r3, ProblemFields: f, WhyNote: "Clearer title."})
	if err != nil {
		t.Fatal(err)
	}
	e.p4exec(t, `UPDATE problems SET current_revision_id = $2 WHERE id = $1`, p3, rev)
	e.p4exec(t, `UPDATE problem_revisions SET vote_count = 5 WHERE id = $1`, rev) // 1 + 5 = 6 votes → +2
	run(a)
	if tier, pts := e.standing(t, a, 1); tier != "domain_contributor" || pts != 12+5+2 {
		t.Fatalf("after current revision: %s %v", tier, pts)
	}

	// Vouches count only from contributors/experts in the domain.
	e.setTier(t, voucher, 1, "domain_expert")
	e.p4exec(t, `INSERT INTO vouches (voucher_id, vouchee_id, domain_id) VALUES ($1, $2, 1), ($3, $2, 1)`, voucher, a, b)
	run(a)
	if _, pts := e.standing(t, a, 1); pts != 19+3 {
		t.Fatalf("after vouches: %v", pts)
	}

	// Idempotent: running again changes nothing (updated_at included).
	var before, after time.Time
	_ = e.st.Pool.QueryRow(ctx, `SELECT updated_at FROM user_domain_standing WHERE user_id = $1 AND domain_id = 1`, a).Scan(&before)
	run(a)
	_ = e.st.Pool.QueryRow(ctx, `SELECT updated_at FROM user_domain_standing WHERE user_id = $1 AND domain_id = 1`, a).Scan(&after)
	if !before.Equal(after) {
		t.Fatal("rerun rewrote an unchanged row")
	}

	// Invalid problems earn nothing: invalidate p2 → loses the +10 → member.
	e.p4exec(t, `UPDATE problems SET state = 'invalid' WHERE id = $1`, p2)
	run(a)
	if tier, pts := e.standing(t, a, 1); tier != "member" || pts != 12 {
		t.Fatalf("after invalid: %s %v", tier, pts)
	}

	// Declared history never adds points; below contributor it gives
	// declared_background.
	e.p4exec(t, `UPDATE users SET declared_history = 'Ran a warehouse for ten years.' WHERE id = $1`, a)
	run(a)
	if tier, pts := e.standing(t, a, 1); tier != "declared_background" || pts != 12 {
		t.Fatalf("declared history: %s %v", tier, pts)
	}

	// Solutions: top solution of a soft-solved problem +5.
	sid, err := e.svc.CreateSolution(ctx, p1, c, service.NewSolution{Kind: store.SolutionKindProcessChange,
		Body: strings.Repeat("Sort the drawings by customer first. ", 3), Display: store.DisplayModeNamed})
	if err != nil {
		t.Fatal(err)
	}
	e.p4exec(t, `UPDATE problems SET soft_solved = true WHERE id = $1`, p1)
	run(c)
	if _, pts := e.standing(t, c, 1); pts != 5 {
		t.Fatalf("top solution: %v (solution %s)", pts, sid)
	}

	// Nightly full pass covers everyone with activity and agrees.
	e.p4exec(t, `DELETE FROM user_domain_standing WHERE user_id IN ($1, $2)`, a, c)
	if err := jobs.RecomputeStanding(ctx, e.deps, nil, 0); err != nil {
		t.Fatal(err)
	}
	if tier, pts := e.standing(t, a, 1); tier != "declared_background" || pts != 12 {
		t.Fatalf("nightly a: %s %v", tier, pts)
	}
	if _, pts := e.standing(t, c, 1); pts != 5 {
		t.Fatalf("nightly c: %v", pts)
	}
}

func TestToggleVouch(t *testing.T) {
	e := p4Env(t, config.RankWeights{}, "")
	ctx := context.Background()
	contrib, member, target := e.user(t), e.user(t), e.user(t)
	e.setTier(t, contrib, 1, "domain_contributor")
	th := handleOf(t, e, target)
	domainSlug := slugOf(t, e, 1)

	cases := []struct {
		name  string
		actor uuid.UUID
		to    string
		slug  string
		want  error
	}{
		{"member cannot vouch", member, th, domainSlug, service.ErrForbidden},
		{"contributor elsewhere cannot vouch here", contrib, th, slugOf(t, e, 2), service.ErrForbidden},
		{"no self-vouch", contrib, handleOf(t, e, contrib), domainSlug, service.ErrForbidden},
		{"unknown user", contrib, "nobody_here", domainSlug, service.ErrNotFound},
		{"unknown domain", contrib, th, "space", service.ErrValidation{}},
		{"contributor vouches", contrib, th, domainSlug, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.svc.ToggleVouch(ctx, tc.actor, tc.to, tc.slug)
			switch want := tc.want.(type) {
			case nil:
				if err != nil {
					t.Fatal(err)
				}
			case service.ErrValidation:
				var ve service.ErrValidation
				if !errors.As(err, &ve) {
					t.Fatalf("want validation, got %v", err)
				}
			default:
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			}
		})
	}
	if n := e.jobCount(t, "recompute_standing", "user_id", target.String()); n != 1 {
		t.Fatalf("vouch enqueued %d standing jobs", n)
	}
	p, err := e.svc.ProfileFor(ctx, th, &contrib)
	if err != nil {
		t.Fatal(err)
	}
	var d service.ProfileDomain
	for _, x := range p.Domains {
		if x.Slug == domainSlug {
			d = x
		}
	}
	if d.Vouches != 1 || !d.ViewerVouched || !d.CanVouch {
		t.Fatalf("profile domain: %+v", d)
	}
	// Toggle off.
	res, err := e.svc.ToggleVouch(ctx, contrib, th, domainSlug)
	if err != nil || res.Vouched {
		t.Fatalf("unvouch: %+v %v", res, err)
	}
	// A signed-out viewer sees counts only and can vouch nowhere.
	p, _ = e.svc.ProfileFor(ctx, th, nil)
	for _, x := range p.Domains {
		if x.CanVouch || x.ViewerVouched || x.Vouches != 0 {
			t.Fatalf("signed-out profile domain: %+v", x)
		}
	}
}

func TestProfileDeclaredHistoryCollapses(t *testing.T) {
	e := p4Env(t, config.RankWeights{}, "")
	ctx := context.Background()
	u := e.user(t)
	h := handleOf(t, e, u)
	e.p4exec(t, `UPDATE users SET declared_history = 'Twenty years in logistics.' WHERE id = $1`, u)
	p, err := e.svc.ProfileByHandle(ctx, h)
	if err != nil || p.DeclaredCollapsed {
		t.Fatalf("new member: collapsed=%v err=%v", p.DeclaredCollapsed, err)
	}
	e.setTier(t, u, 3, "domain_contributor")
	if p, _ = e.svc.ProfileByHandle(ctx, h); !p.DeclaredCollapsed {
		t.Fatal("contributor's declared history should be collapsed")
	}
}

func TestCommunityStateVote(t *testing.T) {
	rw := config.RankWeights{Tiers: map[string]float64{"domain_contributor": 4, "domain_expert": 6}, StateVoteThreshold: 10}
	e := p4Env(t, rw, "{}")
	ctx := context.Background()
	poster, member, c1, c2, x1 := e.user(t), e.user(t), e.user(t), e.user(t), e.user(t)
	e.setTier(t, c1, 1, "domain_contributor")
	e.setTier(t, c2, 1, "domain_contributor")
	e.setTier(t, x1, 1, "domain_expert")
	pid, _ := e.problem(t, poster, "Community vote: spam about crypto coins")

	// Invalid: members can't; contributors can; weights sum to the threshold.
	if _, err := e.svc.ToggleStateVote(ctx, pid, member, store.ProblemStateInvalid); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("member invalid vote: %v", err)
	}
	if err := e.svc.ChangeProblemState(ctx, pid, poster, store.ProblemStateInvalid, ""); err == nil {
		t.Fatal("poster cannot mark invalid")
	}
	res, err := e.svc.ToggleStateVote(ctx, pid, c1, store.ProblemStateInvalid)
	if err != nil || !res.Voted || res.Percent != 40 || res.Flipped {
		t.Fatalf("first vote: %+v %v", res, err)
	}
	// Toggle off and on again.
	if res, err = e.svc.ToggleStateVote(ctx, pid, c1, store.ProblemStateInvalid); err != nil || res.Voted || res.Percent != 0 {
		t.Fatalf("withdraw: %+v %v", res, err)
	}
	if _, err = e.svc.ToggleStateVote(ctx, pid, c1, store.ProblemStateInvalid); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ChangeProblemState(ctx, pid, c2, store.ProblemStateInvalid, ""); err != nil {
		t.Fatal(err)
	}
	pg, err := e.svc.ProblemPage(ctx, pid, &x1)
	if err != nil {
		t.Fatal(err)
	}
	if pg.Community.InvalidPercent != 80 || !pg.Community.CanVoteInvalid || pg.Community.CanVoteSolved {
		t.Fatalf("page community view: %+v", pg.Community)
	}
	if res, err = e.svc.ToggleStateVote(ctx, pid, x1, store.ProblemStateInvalid); err != nil || !res.Flipped {
		t.Fatalf("threshold vote: %+v %v", res, err)
	}
	pg, _ = e.svc.ProblemPage(ctx, pid, nil)
	if pg.Problem.State != store.ProblemStateInvalid {
		t.Fatalf("state %s", pg.Problem.State)
	}
	last := pg.History[len(pg.History)-1]
	if last.ActorKind != "system" || last.Reason != "Community vote" || last.ToState != store.ProblemStateInvalid {
		t.Fatalf("event: %+v", last)
	}
	var actor *uuid.UUID
	if err := e.st.Pool.QueryRow(ctx, `SELECT actor_id FROM problem_state_events WHERE problem_id = $1 ORDER BY created_at DESC LIMIT 1`, pid).Scan(&actor); err != nil || actor != nil {
		t.Fatalf("actor_id %v err %v", actor, err)
	}
	var left int
	_ = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM problem_state_votes WHERE problem_id = $1`, pid).Scan(&left)
	if left != 0 {
		t.Fatalf("%d votes left after the flip", left)
	}
	if n := e.jobCount(t, "recompute_standing", "user_id", poster.String()); n < 1 {
		t.Fatal("flip did not enqueue RecomputeStanding for the poster")
	}
	if _, err := e.svc.ToggleStateVote(ctx, pid, c1, store.ProblemStateInvalid); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("vote on invalid: %v", err)
	}

	// Solved: blocked until the poster has been silent for 14 days.
	p2, r2 := e.problem(t, poster, "Community vote: poster went quiet on shift rotas")
	if err := e.svc.ChangeProblemState(ctx, p2, member, store.ProblemStateSolved, ""); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("early solved vote: %v", err)
	}
	real := e.svc.Now
	e.svc.Now = func() time.Time { return real().Add(15 * 24 * time.Hour) }
	defer func() { e.svc.Now = real }()
	// A poster revision restarts the clock.
	f := sampleProblem("").ProblemFields
	f.Title = "Community vote: poster came back with details"
	if _, err := e.svc.CreateRevision(ctx, p2, poster, service.NewRevision{ParentRevisionID: r2, ProblemFields: f, WhyNote: "More detail."}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ToggleStateVote(ctx, p2, member, store.ProblemStateSolved); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("solved vote right after a poster revision: %v", err)
	}
	e.svc.Now = func() time.Time { return real().Add(30 * 24 * time.Hour) }
	if res, err = e.svc.ToggleStateVote(ctx, p2, member, store.ProblemStateSolved); err != nil || !res.Voted || res.Percent != 10 {
		t.Fatalf("solved vote after silence: %+v %v", res, err)
	}
	pg, _ = e.svc.ProblemPage(ctx, p2, &c1)
	if !pg.Community.CanVoteSolved || pg.Community.SolvedPercent != 10 {
		t.Fatalf("solved view: %+v", pg.Community)
	}

	// Admin zero-votes zeroes community state vote weights too.
	if _, _, err := e.svc.AdminZeroVotes(ctx, poster, handleOf(t, e, member)); err != nil {
		t.Fatal(err)
	}
	var w float32
	if err := e.st.Pool.QueryRow(ctx, `SELECT weight FROM problem_state_votes WHERE problem_id = $1 AND user_id = $2`, p2, member).Scan(&w); err != nil || w != 0 {
		t.Fatalf("zeroed weight %v err %v", w, err)
	}
}

func handleOf(t *testing.T, e *contentEnv, id uuid.UUID) string {
	t.Helper()
	u, err := e.svc.GetUser(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u.Handle
}

func slugOf(t *testing.T, e *contentEnv, domain int16) string {
	t.Helper()
	var s string
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT slug FROM domains WHERE id = $1`, domain).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}
