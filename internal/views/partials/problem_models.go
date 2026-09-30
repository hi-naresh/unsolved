// Package partials holds htmx-swappable fragments shared by pages. The view
// models here carry views.AuthorRef, never author ids.
package partials

import (
	"time"

	"github.com/hi-naresh/unsolved/internal/views"
)

// VoteMode says how a vote control renders for this viewer.
type VoteMode int

const (
	VoteLive   VoteMode = iota // a working toggle
	VoteSignIn                 // signed out: links to sign-in
	VoteStatic                 // own revision or invalid problem: count only
)

// VoteButtonView is one vote control (problem or solution revision).
type VoteButtonView struct {
	URL       string // POST target: /r/{id}/vote or /sr/{id}/vote
	Voted     bool
	Count     int32
	Mode      VoteMode
	SignInURL string // VoteSignIn only
	Note      string // VoteStatic tooltip
}

// ProblemRowView is one line of a problem list.
type ProblemRowView struct {
	ID         string
	Title      string
	DomainName string
	DomainSlug string
	State      string
	SoftSolved bool
	Pinned     bool
	Votes      int32
	Author     views.AuthorRef
	CreatedAt  time.Time
}

// ProblemRowsView is one keyset page of rows plus the "more" link.
type ProblemRowsView struct {
	Rows    []ProblemRowView
	MoreURL string // empty on the last page
	Empty   string // shown when the first page has no rows
}

// ProblemLinkView links to another problem (forks).
type ProblemLinkView struct {
	ID    string
	Title string
}

// InterestView is the founder-interest toggle.
type InterestView struct {
	ProblemID  string
	Count      int64
	Interested bool
	Mode       VoteMode
	SignInURL  string
}

// TrialNoteView is one recent "tried it" note (no author: trials are private).
type TrialNoteView struct {
	Outcome string
	Note    string
}

// SolutionView is one solution on the problem page.
type SolutionView struct {
	ID            string
	ProblemID     string
	RevisionID    string
	Kind          string
	Body          string
	IsRevised     bool
	Author        views.AuthorRef
	Vote          VoteButtonView
	Worked        int64
	Partly        int64
	Failed        int64
	Notes         []TrialNoteView
	ViewerOutcome string
	CanWrite      bool // signed-in writer on a non-invalid problem
	Handle        string
	Display       string // default posting-as mode
}

// SolutionKinds are the kind values in form order.
var SolutionKinds = []string{"process_change", "off_the_shelf", "custom_software", "dont_automate"}

// SolutionKindLabel is the human label of a solution kind.
func SolutionKindLabel(kind string) string {
	switch kind {
	case "process_change":
		return "Change the process"
	case "off_the_shelf":
		return "Use an existing tool"
	case "custom_software":
		return "Build something custom"
	case "dont_automate":
		return "Don't automate this"
	}
	return kind
}

// OutcomeLabel is the human label of a "tried it" outcome.
func OutcomeLabel(o string) string {
	switch o {
	case "worked":
		return "Worked"
	case "partly":
		return "Partly worked"
	case "failed":
		return "Didn't work"
	}
	return o
}

// ProblemStateLabel is the human label of a problem state.
func ProblemStateLabel(s string) string {
	switch s {
	case "open":
		return "Open"
	case "solved":
		return "Solved"
	case "invalid":
		return "Invalid"
	}
	return s
}

// ProblemDate formats a timestamp for display.
func ProblemDate(t time.Time) string { return t.UTC().Format("2 Jan 2006") }
