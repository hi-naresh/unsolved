package http

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/store"
)

// p4SetTier gives a user a standing tier in a domain directly (setup only).
func p4SetTier(t *testing.T, app *testApp, user uuid.UUID, domain int16, tier string) {
	t.Helper()
	execSQL(t, app, `INSERT INTO user_domain_standing (user_id, domain_id, tier) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, domain_id) DO UPDATE SET tier = EXCLUDED.tier`, user, domain, tier)
}

// Phase 4 acceptance: tier beside names and on anonymous posts; vouching;
// a weighted community vote flips a problem invalid with a system event.
func TestPhase4Reputation(t *testing.T) {
	app := newTestApp(t, func(c *config.Config) {
		c.RankWeightsJSON = `{"tiers":{"domain_contributor":5,"domain_expert":8},"state_vote_threshold":10}`
		c.RankWeights = config.RankWeights{
			Tiers:              map[string]float64{"domain_contributor": 5, "domain_expert": 8},
			StateVoteThreshold: 10,
		}
	})
	const logistics = 3 // problemDirect posts in domain 3

	// --- tier shown on an anonymous post (and beside a named one) ---
	expert := app.newUser(t)
	p4SetTier(t, app, expert.ID, logistics, "domain_expert")
	anonID := problemDirect(t, app, expert.ID, "Phase four: anonymous expert on pallet labels", store.DisplayModeAnonymous).String()
	namedID := problemDirect(t, app, expert.ID, "Phase four: named expert on yard scheduling", store.DisplayModeNamed).String()
	c := app.anon(t)
	b := mustStatus(t, c.get("/p/"+anonID), http.StatusOK)
	if !strings.Contains(b, "Anonymous <span class=\"tier\">· domain expert</span>") || strings.Contains(b, "@"+expert.Handle) {
		t.Fatal("anonymous post lacks the tier or leaks the handle")
	}
	b = mustStatus(t, c.get("/p/"+namedID), http.StatusOK)
	if !strings.Contains(b, "@"+expert.Handle) || !strings.Contains(b, "· domain expert") {
		t.Fatal("named post lacks the tier beside the handle")
	}
	b = mustStatus(t, c.get("/problems?domain=logistics&sort=new"), http.StatusOK)
	if !strings.Contains(b, "domain expert") {
		t.Fatal("problem list lacks the tier")
	}
	b = mustStatus(t, c.get("/p/"+anonID+"/evolution"), http.StatusOK)
	if !strings.Contains(b, "domain expert") || strings.Contains(b, "@"+expert.Handle) {
		t.Fatal("evolution lacks the tier on the anonymous revision")
	}

	// --- vouching ---
	voucher, vouchee, member := app.newUser(t), app.newUser(t), app.newUser(t)
	p4SetTier(t, app, voucher.ID, logistics, "domain_contributor")
	vc, mc := app.as(t, voucher), app.as(t, member)
	b = mustStatus(t, vc.get("/u/"+vouchee.Handle), http.StatusOK)
	if !strings.Contains(b, "Vouch in Logistics") || strings.Contains(b, "Vouch in Retail") {
		t.Fatal("contributor sees no vouch form (or one for the wrong domain)")
	}
	if b = mustStatus(t, mc.get("/u/"+vouchee.Handle), http.StatusOK); strings.Contains(b, "Vouch in") {
		t.Fatal("a member sees a vouch form")
	}
	mustStatus(t, mc.postForm("/u/"+vouchee.Handle+"/vouch", url.Values{"domain": {"logistics"}}), http.StatusForbidden)
	mustStatus(t, vc.postForm("/u/"+voucher.Handle+"/vouch", url.Values{"domain": {"logistics"}}), http.StatusForbidden)
	mustStatus(t, app.anon(t).postForm("/u/"+vouchee.Handle+"/vouch", url.Values{"domain": {"logistics"}}), http.StatusUnauthorized)
	resp := vc.postForm("/u/"+vouchee.Handle+"/vouch", url.Values{"domain": {"logistics"}})
	mustStatus(t, resp, http.StatusSeeOther)
	if loc := resp.Header.Get("Location"); loc != "/u/"+vouchee.Handle+"#vouches" {
		t.Fatalf("vouch redirect %q", loc)
	}
	b = mustStatus(t, app.anon(t).get("/u/"+vouchee.Handle), http.StatusOK)
	if !strings.Contains(b, "Vouched by 1 member in Logistics") || strings.Contains(b, voucher.Handle) {
		t.Fatal("profile lacks the vouch count, or names the voucher")
	}
	if b = mustStatus(t, vc.get("/u/"+vouchee.Handle), http.StatusOK); !strings.Contains(b, "Withdraw vouch in Logistics") {
		t.Fatal("voucher cannot withdraw")
	}
	// RecomputeStanding ran for the vouchee: +3 per vouch.
	eventually(t, 10*time.Second, func() bool {
		var pts float64
		err := app.Store.Pool.QueryRow(context.Background(),
			`SELECT points FROM user_domain_standing WHERE user_id = $1 AND domain_id = $2`, vouchee.ID, logistics).Scan(&pts)
		return err == nil && pts == 3
	})

	// --- community vote: invalid ---
	poster, c1, c2 := app.newUser(t), app.newUser(t), app.newUser(t)
	p4SetTier(t, app, c1.ID, logistics, "domain_contributor")
	p4SetTier(t, app, c2.ID, logistics, "domain_contributor")
	pid := problemDirect(t, app, poster.ID, "Phase four: buy cheap watches here now", store.DisplayModeNamed).String()
	c1c, c2c := app.as(t, c1), app.as(t, c2)
	if b = mustStatus(t, mc.get("/p/"+pid), http.StatusOK); strings.Contains(b, "Vote to mark invalid") {
		t.Fatal("a member sees the invalid vote button")
	}
	mustStatus(t, mc.postForm("/p/"+pid+"/state", url.Values{"to": {"invalid"}}), http.StatusForbidden)
	mustStatus(t, mc.postForm("/p/"+pid+"/state", url.Values{"to": {"solved"}}), http.StatusForbidden) // poster not silent
	if b = mustStatus(t, c1c.get("/p/"+pid), http.StatusOK); !strings.Contains(b, "Vote to mark invalid") {
		t.Fatal("a contributor lacks the invalid vote button")
	}
	mustStatus(t, c1c.postForm("/p/"+pid+"/state", url.Values{"to": {"invalid"}}), http.StatusSeeOther)
	b = mustStatus(t, app.anon(t).get("/p/"+pid), http.StatusOK)
	if !strings.Contains(b, "Community vote to mark invalid: 50% of the way") {
		t.Fatal("tally not shown as a fraction of the threshold")
	}
	if b = mustStatus(t, c1c.get("/p/"+pid), http.StatusOK); !strings.Contains(b, "Withdraw your vote (invalid)") {
		t.Fatal("voter cannot withdraw")
	}
	mustStatus(t, c2c.postForm("/p/"+pid+"/state", url.Values{"to": {"invalid"}}), http.StatusSeeOther)
	b = mustStatus(t, app.anon(t).get("/p/"+pid), http.StatusOK)
	if !strings.Contains(b, "marked invalid") || !strings.Contains(b, "by the community") || !strings.Contains(b, "Community vote") {
		t.Fatal("community flip not shown")
	}
	var actor *uuid.UUID
	var reason string
	if err := app.Store.Pool.QueryRow(context.Background(),
		`SELECT actor_id, reason FROM problem_state_events WHERE problem_id = $1 AND to_state = 'invalid'`, pid).Scan(&actor, &reason); err != nil {
		t.Fatal(err)
	}
	if actor != nil || reason != "Community vote" {
		t.Fatalf("event actor %v reason %q", actor, reason)
	}
}
